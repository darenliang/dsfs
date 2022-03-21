package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/darenliang/dsfs/fuse"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Dsfs struct {
	fuse.FileSystemBase
	lock sync.Mutex
	dg   *discordgo.Session
	db   *DB
	open map[string]*FileData
}

type FileData struct {
	lock    sync.RWMutex
	syncing bool
	data    []byte
	dirty   bool
	mtim    time.Time
	ctim    time.Time
}

func getDir(path string) string {
	return strings.ReplaceAll(filepath.Dir(path), "\\", "/")
}

func (fs *Dsfs) Mknod(path string, mode uint32, dev uint64) int {
	fmt.Println("Mknod", path, mode, dev)
	fs.lock.Lock()
	defer fs.lock.Unlock()

	// Check open map
	if _, ok := fs.open[path]; ok {
		return -fuse.EEXIST
	}

	// Check parent in db
	if _, ok := fs.db.radix.Get([]byte(getDir(path))); !ok {
		return -fuse.ENOENT
	}

	// Check file in db
	if _, ok := fs.db.radix.Get([]byte(path)); ok {
		return -fuse.EEXIST
	}

	fs.open[path] = &FileData{
		data: make([]byte, 0),
		mtim: time.Now(),
		ctim: time.Now(),
	}

	return 0
}

func (fs *Dsfs) Mkdir(path string, mode uint32) int {
	fmt.Println("Mkdir", path, mode)
	pathBytes := []byte(path)

	fs.lock.Lock()
	// Check parent in db
	if _, ok := fs.db.radix.Get([]byte(getDir(path))); !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}

	// Check file in db
	if _, ok := fs.db.radix.Get(pathBytes); ok {
		fs.lock.Unlock()
		return -fuse.EEXIST
	}

	// Make tx
	tx := Tx{
		Tx:   WriteTx,
		Path: path,
		Type: FolderType,
	}
	fs.db.radix, _, _ = fs.db.radix.Insert(pathBytes, tx)
	fs.lock.Unlock()

	b, _ := json.Marshal(tx)
	if len(b) > MaxDiscordFileSize {
		return -fuse.EACCES
	}
	_, err := fs.dg.ChannelFileSend(fs.db.txChannelID, TxChannelName, bytes.NewReader(b))
	if err != nil {
		return -fuse.EACCES
	}

	return 0
}

func (fs *Dsfs) Open(path string, flags int) (int, uint64) {
	fmt.Println("Open", path, flags)

	fs.lock.Lock()

	// Check open map
	if _, ok := fs.open[path]; ok {
		fs.lock.Unlock()
		return 0, 1
	}

	val, ok := fs.db.radix.Get([]byte(path))
	if !ok {
		fs.lock.Unlock()
		return -fuse.EEXIST, ^uint64(0)
	}
	tx := val.(Tx)

	if tx.Type == FolderType {
		fs.lock.Unlock()
		return 0, 1
	}

	fs.open[path] = &FileData{
		data: make([]byte, 0),
		mtim: tx.Stat.Mtim,
		ctim: tx.Stat.Ctim,
	}
	fs.lock.Unlock()

	go func() {
		fs.open[path].lock.Lock()
		defer fs.open[path].lock.Unlock()
		for _, id := range tx.FileIDs {
			b, err := getDataFile(fs.db.dataChannelID, id)
			if err != nil {
				return
			}
			fs.open[path].data = append(fs.open[path].data, b...)
		}
	}()

	return 0, 1
}

func (fs *Dsfs) Unlink(path string) int {
	fmt.Println("Unlink", path)

	fs.lock.Lock()

	pathBytes := []byte(path)
	val, ok := fs.db.radix.Get(pathBytes)
	if !ok {
		// If only found in mem, just drop the file data
		if _, ok := fs.open[path]; ok {
			delete(fs.open, path)
			fs.lock.Unlock()
			return 0
		}
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	tx := val.(Tx)
	if tx.Type == FolderType {
		fs.lock.Unlock()
		return -fuse.EISDIR
	}

	delete(fs.open, path)
	fs.db.radix, _, _ = fs.db.radix.Delete(pathBytes)
	fs.lock.Unlock()

	b, _ := json.Marshal(createDeleteTx(path))
	if len(b) > MaxDiscordFileSize {
		return -fuse.EACCES
	}
	_, err := fs.dg.ChannelFileSend(fs.db.txChannelID, TxChannelName, bytes.NewReader(b))
	if err != nil {
		return -fuse.EACCES
	}

	return 0
}

func (fs *Dsfs) Rmdir(path string) int {
	fmt.Println("Rmdir", path)

	fs.lock.Lock()

	pathBytes := []byte(path)
	val, ok := fs.db.radix.Get(pathBytes)
	if !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	tx := val.(Tx)
	if tx.Type != FolderType {
		fs.lock.Unlock()
		return -fuse.ENOTDIR
	}

	it := fs.db.radix.Root().Iterator()
	it.SeekPrefix(pathBytes)
	for key, _, ok := it.Next(); ok; key, _, ok = it.Next() {
		if bytes.Compare(key, pathBytes) != 0 {
			fs.lock.Unlock()
			return -fuse.ENOTEMPTY
		}
	}
	fs.db.radix, _, _ = fs.db.radix.Delete(pathBytes)
	fs.lock.Unlock()

	b, _ := json.Marshal(createDeleteTx(path))
	if len(b) > MaxDiscordFileSize {
		return -fuse.EACCES
	}
	_, err := fs.dg.ChannelFileSend(fs.db.txChannelID, TxChannelName, bytes.NewReader(b))
	if err != nil {
		return -fuse.EACCES
	}

	return 0
}

func (fs *Dsfs) Rename(oldpath string, newpath string) int {
	fmt.Println("Rename", oldpath, newpath)

	fs.lock.Lock()

	if _, ok := fs.db.radix.Get([]byte(getDir(newpath))); !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}

	oldpathBytes := []byte(oldpath)
	newpathBytes := []byte(newpath)
	val, ok := fs.db.radix.Get(oldpathBytes)
	if !ok {
		// If only found in mem, just rename the file directly
		if val, ok = fs.open[oldpath]; ok {
			tmp := fs.open[oldpath]
			delete(fs.open, oldpath)
			fs.open[newpath] = tmp
			fs.lock.Unlock()
			return 0
		}
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	tx := val.(Tx)

	if tx.Type == FolderType {
		key, _, _ := fs.db.radix.Root().LongestPrefix(oldpathBytes)
		fmt.Println("Compare", string(key), string(oldpathBytes))
		if bytes.Compare(key, oldpathBytes) != 0 {
			fs.lock.Unlock()
			return -fuse.ENOTEMPTY
		}
	} else {
		// Check mem for files
		if val, ok = fs.open[oldpath]; ok {
			tmp := fs.open[oldpath]
			delete(fs.open, oldpath)
			fs.open[newpath] = tmp
		}
	}
	fs.lock.Unlock()

	tx.Path = newpath
	b, _ := json.Marshal(tx)
	if len(b) > MaxDiscordFileSize {
		return -fuse.EACCES
	}
	_, err := fs.dg.ChannelFileSend(fs.db.txChannelID, TxChannelName, bytes.NewReader(b))
	if err != nil {
		return -fuse.EACCES
	}
	fs.db.radix, _, _ = fs.db.radix.Insert(newpathBytes, tx)

	b, _ = json.Marshal(createDeleteTx(oldpath))
	if len(b) > MaxDiscordFileSize {
		return -fuse.EACCES
	}
	_, err = fs.dg.ChannelFileSend(fs.db.txChannelID, TxChannelName, bytes.NewReader(b))
	if err != nil {
		return -fuse.EACCES
	}
	fs.db.radix, _, _ = fs.db.radix.Delete(oldpathBytes)

	return 0
}

func (fs *Dsfs) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	fmt.Println("Getattr", path, fh)

	fs.lock.Lock()
	defer fs.lock.Unlock()

	// Check open map
	if _, ok := fs.open[path]; ok {
		stat.Mode = fuse.S_IFREG | 0777
		stat.Size = int64(len(fs.open[path].data))
		stat.Ctim = fuse.NewTimespec(fs.open[path].ctim)
		stat.Mtim = fuse.NewTimespec(fs.open[path].mtim)
		return 0
	}

	val, ok := fs.db.radix.Get([]byte(path))
	if !ok {
		return -fuse.ENOENT
	}
	tx := val.(Tx)
	if tx.Type == FileType {
		tx := val.(Tx)
		stat.Mode = fuse.S_IFREG | 0777
		stat.Size = tx.Stat.Size
		stat.Ctim = fuse.NewTimespec(tx.Stat.Ctim)
		stat.Mtim = fuse.NewTimespec(tx.Stat.Mtim)
	} else {
		stat.Mode = fuse.S_IFDIR | 0777
	}
	return 0
}

func (fs *Dsfs) Truncate(path string, size int64, fh uint64) int {
	fmt.Println("Truncate", path, size, fh)

	fs.lock.Lock()

	if _, ok := fs.open[path]; !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}

	filesize := int64(len(fs.open[path].data))
	fs.lock.Unlock()

	fs.open[path].lock.Lock()
	if size == filesize {
		fs.open[path].lock.Unlock()
		return 0
	} else if size < filesize {
		fs.open[path].data = fs.open[path].data[:size]
	} else {
		fs.open[path].data = append(fs.open[path].data, make([]byte, size-filesize)...)
	}

	fs.open[path].mtim = time.Now()
	fs.open[path].lock.Unlock()

	return 0
}

func (fs *Dsfs) Read(path string, buff []byte, ofst int64, fh uint64) int {
	fmt.Println("Read", path, len(buff), ofst, fh)

	fs.lock.Lock()

	if _, ok := fs.open[path]; !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}

	filesize := int64(len(fs.open[path].data))
	endofst := ofst + int64(len(buff))
	fs.lock.Unlock()

	fs.open[path].lock.RLock()
	if endofst > filesize {
		endofst = filesize
	}
	if endofst < ofst {
		fs.open[path].lock.RUnlock()
		return 0
	}

	bytesRead := copy(buff, fs.open[path].data[ofst:endofst])
	fs.open[path].lock.RUnlock()

	return bytesRead
}

func (fs *Dsfs) Write(path string, buff []byte, ofst int64, fh uint64) int {
	fmt.Println("Write", path, len(buff), ofst, fh)

	fs.lock.Lock()

	if _, ok := fs.open[path]; !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}

	filesize := int64(len(fs.open[path].data))
	endofst := ofst + int64(len(buff))
	fs.lock.Unlock()

	fs.open[path].lock.Lock()
	if endofst > filesize {
		fs.open[path].data = append(fs.open[path].data, make([]byte, endofst-filesize)...)
	}

	bytesWrite := copy(fs.open[path].data[ofst:endofst], buff)
	fs.open[path].mtim = time.Now()
	fs.open[path].dirty = true
	fs.open[path].lock.Unlock()

	return bytesWrite
}

func (fs *Dsfs) Release(path string, fh uint64) int {
	fmt.Println("Release", path, fh)

	fs.lock.Lock()

	file, ok := fs.open[path]
	if !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	if !file.dirty {
		// TODO: find a good way to evict stale files from cache
		// delete(fs.open, path)
		fs.lock.Unlock()
		return 0
	}

	tx := Tx{
		Tx:      WriteTx,
		Path:    path,
		Type:    FileType,
		FileIDs: make([]string, 0),
		Stat: Stat{
			Size: int64(len(file.data)),
			Mtim: file.mtim,
			Ctim: file.ctim,
		},
	}
	fs.lock.Unlock()

	go func() {
		fmt.Printf("Uploading %s in the background", path)
		if file.syncing {
			return
		}
		file.lock.RLock()
		file.syncing = true
		file.dirty = false
		defer file.lock.RUnlock()
		defer func() { file.syncing = false }()
		cursor := 0
		for cursor < len(file.data) {
			fmt.Println("cursor", cursor)
			end := cursor + MaxDiscordFileSize
			if end > len(file.data) {
				end = len(file.data)
			}
			msg, err := fs.dg.ChannelFileSend(fs.db.dataChannelID, DataChannelName, bytes.NewReader(file.data[cursor:end]))
			if err != nil {
				return
			}
			tx.FileIDs = append(tx.FileIDs, msg.Attachments[0].ID)
			cursor += MaxDiscordFileSize
		}

		b, _ := json.Marshal(tx)
		if len(b) > MaxDiscordFileSize {
			return
		}
		_, err := fs.dg.ChannelFileSend(fs.db.txChannelID, TxChannelName, bytes.NewReader(b))
		if err != nil {
			return
		}
		fs.db.radix, _, _ = fs.db.radix.Insert([]byte(path), tx)
	}()

	// TODO: find a good way to evict stale files from cache
	// delete(fs.open, path)
	return 0
}

func (fs *Dsfs) Opendir(path string) (int, uint64) {
	fmt.Println("Opendir", path)
	return 0, 1
}

func (fs *Dsfs) Readdir(
	path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64,
	fh uint64,
) int {
	fmt.Println("Readdir", path, ofst, fh)

	fs.lock.Lock()
	defer fs.lock.Unlock()

	fill(".", &fuse.Stat_t{Mode: fuse.S_IFDIR | 0777}, 0)
	fill("..", nil, 0)
	it := fs.db.radix.Root().Iterator()
	it.SeekPrefix([]byte(path))
	for key, val, ok := it.Next(); ok; key, _, ok = it.Next() {
		subpath := string(key)
		// File is already open
		if _, ok := fs.open[subpath]; ok {
			continue
		}
		if subpath == path || getDir(subpath) != path {
			continue
		}
		name := filepath.Base(subpath)
		tx := val.(Tx)
		if tx.Type == FileType {
			fill(name, &fuse.Stat_t{
				Mode: fuse.S_IFREG | 0777,
				Size: tx.Stat.Size,
				Ctim: fuse.NewTimespec(tx.Stat.Ctim),
				Mtim: fuse.NewTimespec(tx.Stat.Mtim),
			}, 0)
		} else {
			fill(name, &fuse.Stat_t{Mode: fuse.S_IFDIR | 0777}, 0)
		}
	}
	for key, val := range fs.open {
		if getDir(key) != path {
			continue
		}
		name := filepath.Base(key)
		fill(name, &fuse.Stat_t{
			Mode: fuse.S_IFREG | 0777,
			Size: int64(len(val.data)),
			Ctim: fuse.NewTimespec(val.ctim),
			Mtim: fuse.NewTimespec(val.mtim),
		}, 0)
	}
	return 0
}

func (fs *Dsfs) Releasedir(path string, fh uint64) int {
	fmt.Println("Releasedir", path, fh)
	return 0
}

func (fs *Dsfs) Statfs(path string, stat *fuse.Statfs_t) int {
	fmt.Println("Statfs", path)
	stat.Bsize = 4096
	stat.Frsize = stat.Bsize
	stat.Blocks = 256 * 1024 * 1024
	stat.Bfree = stat.Blocks
	stat.Bavail = stat.Blocks
	return 0
}

func NewDsfs(dg *discordgo.Session, db *DB) *Dsfs {
	dsfs := Dsfs{}
	dsfs.dg = dg
	dsfs.db = db
	dsfs.open = make(map[string]*FileData)
	return &dsfs
}
