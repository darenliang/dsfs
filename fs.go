package main

import (
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/darenliang/dsfs/fuse"
	"go.uber.org/zap"
)

type Dsfs struct {
	fuse.FileSystemBase
	dg          *discordgo.Session
	db          DB
	writer      *Writer
	txChannel   *discordgo.Channel
	dataChannel *discordgo.Channel
	open        map[string]*FileData
	lock        sync.Mutex
	cacheType   string
}

type FileData struct {
	mtim    time.Time
	ctim    time.Time
	syncing *atomic.Bool
	load    *Load
	cache   Cache
	lock    sync.RWMutex
	dirty   bool
}

func NewDsfs(dg *discordgo.Session, db DB, writer *Writer, txChannel *discordgo.Channel, dataChannel *discordgo.Channel, cacheType string) *Dsfs {
	dsfs := Dsfs{}
	dsfs.dg = dg
	dsfs.db = db
	dsfs.writer = writer
	dsfs.txChannel = txChannel
	dsfs.dataChannel = dataChannel
	dsfs.open = make(map[string]*FileData)
	dsfs.cacheType = cacheType
	return &dsfs
}

func (fs *Dsfs) Mknod(path string, mode uint32, dev uint64) int {
	zap.S().Debugw("Mknod",
		"path", path, "mode", mode, "dev", dev,
	)
	fs.lock.Lock()
	defer fs.lock.Unlock()

	// Check open map
	if _, ok := fs.open[path]; ok {
		return -fuse.EEXIST
	}

	// Check parent in db
	if _, ok := fs.db.Get(getDir(path)); !ok {
		return -fuse.ENOENT
	}

	// Check file in db
	if _, ok := fs.db.Get(path); ok {
		return -fuse.EEXIST
	}

	fs.open[path] = &FileData{
		cache:   fs.GetNewCache(),
		load:    newLoad(),
		syncing: &atomic.Bool{},
		mtim:    time.Now(),
		ctim:    time.Now(),
	}

	return 0
}

func (fs *Dsfs) Mkdir(path string, mode uint32) int {
	zap.S().Debugw("Mkdir", "path", path, "mode", mode)
	fs.lock.Lock()

	// Check parent in db
	if _, ok := fs.db.Get(getDir(path)); !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}

	// Check file in db
	if _, ok := fs.db.Get(path); ok {
		fs.lock.Unlock()
		return -fuse.EEXIST
	}

	// Make tx
	tx := &Tx{
		Tx:   WriteTx,
		Path: path,
		Type: FolderType,
	}
	fs.db.Insert(path, tx)
	fs.lock.Unlock()

	b, _ := json.Marshal(tx)
	if len(b) > MaxDiscordFileSize {
		return -fuse.EACCES
	}

	go func() { fs.writer.SendTx(b) }()

	return 0
}

func (fs *Dsfs) Open(path string, flags int) (int, uint64) {
	zap.S().Debugw("Open", "path", path, "flags", flags)
	fs.lock.Lock()

	// Check open map
	if _, ok := fs.open[path]; ok {
		fs.lock.Unlock()
		return 0, 1
	}

	tx, ok := fs.db.Get(path)
	if !ok {
		fs.lock.Unlock()
		return -fuse.EEXIST, ^uint64(0)
	}

	if tx.Type == FolderType {
		fs.lock.Unlock()
		return 0, 1
	}

	cache := fs.GetNewCache()
	cache.Truncate(tx.Size)
	fs.open[path] = &FileData{
		cache:   cache,
		load:    newLoad(),
		syncing: &atomic.Bool{},
		mtim:    tx.Mtim,
		ctim:    tx.Ctim,
	}
	fs.lock.Unlock()

	// Load entire file in mem in the background.
	// This solution is really hacky and isn't great and loading files
	// incrementally is preferable.
	//
	// If memory is really constrained, it is better to save files in a folder
	// other than the root folder, since some file explorers like to eagerly
	// prob files for thumbnails, etc.
	go func() {
		buffer := make([]byte, FileBlockSize)

		dlID := func(id string, ofst int) error {
			file, ok := fs.open[path]
			if !ok {
				err := errors.New("file no longer exists")
				zap.S().Warn(err)
				return err
			}
			n, err := getDataFile(fs.dataChannel.ID, id, buffer)
			if err != nil {
				zap.S().Warnw("network error with Discord", "error", err)
				return err
			}
			file.lock.Lock()
			file.cache.WriteRange(int64(ofst), int64(ofst+n), buffer[:n])
			file.load.addRange(int64(ofst), int64(ofst+n))
			file.lock.Unlock()
			return nil
		}

		// Quick check to see if file parts exist
		if len(tx.FileIDs) == 0 {
			return
		}

		// Download first piece
		err := dlID(tx.FileIDs[0], 0)
		if err != nil {
			return
		}

		// Quick check to see if there is only one file part
		if len(tx.FileIDs) == 1 {
			return
		}

		// Download last piece to simulate torrent streaming behavior
		lastIdx := len(tx.FileIDs) - 1
		err = dlID(tx.FileIDs[lastIdx], lastIdx*FileBlockSize)
		if err != nil {
			return
		}

		// Download rest in order
		for i := 1; i < lastIdx; i++ {
			err = dlID(tx.FileIDs[i], i*FileBlockSize)
			if err != nil {
				return
			}
		}
	}()

	return 0, 1
}

func (fs *Dsfs) Unlink(path string) int {
	zap.S().Debugw("Unlink", "path", path)
	fs.lock.Lock()

	tx, ok := fs.db.Get(path)
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
	if tx.Type == FolderType {
		fs.lock.Unlock()
		return -fuse.EISDIR
	}

	delete(fs.open, path)
	fs.db.Delete(path)
	fs.lock.Unlock()

	b, _ := json.Marshal(createDeleteTx(path))
	if len(b) > MaxDiscordFileSize {
		return -fuse.EACCES
	}

	go func() { fs.writer.SendTx(b) }()

	return 0
}

func (fs *Dsfs) Rmdir(path string) int {
	zap.S().Debugw("Rmdir", "path", path)
	fs.lock.Lock()

	tx, ok := fs.db.Get(path)
	if !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	if tx.Type != FolderType {
		fs.lock.Unlock()
		return -fuse.ENOTDIR
	}

	// Hacky way to find if a folder is empty or not. Not well tested.
	it := fs.db.Iterator(path)
	for key, _, ok := it.Next(); ok; key, _, ok = it.Next() {
		if key != path {
			fs.lock.Unlock()
			return -fuse.ENOTEMPTY
		}
	}
	fs.db.Delete(path)
	fs.lock.Unlock()

	b, _ := json.Marshal(createDeleteTx(path))
	if len(b) > MaxDiscordFileSize {
		return -fuse.EACCES
	}

	go func() { fs.writer.SendTx(b) }()

	return 0
}

func (fs *Dsfs) Rename(oldpath string, newpath string) int {
	zap.S().Debugw("Rename",
		"oldpath", oldpath, "newpath", newpath,
	)
	fs.lock.Lock()

	if _, ok := fs.db.Get(getDir(newpath)); !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}

	tx, ok := fs.db.Get(oldpath)
	if !ok {
		// If only found in mem, just rename the file directly
		if val, ok := fs.open[oldpath]; ok {
			fs.open[newpath] = val
			delete(fs.open, oldpath)
			fs.lock.Unlock()
			return 0
		}
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	if tx.Type == FolderType {
		// The same hacky way used for Rmdir.
		// It's pretty sad that renames won't work because paths are hardcoded.
		//
		// Looking for a better solution that would eliminate the need of
		// hardcoded paths.
		it := fs.db.Iterator(oldpath)
		for key, _, ok := it.Next(); ok; key, _, ok = it.Next() {
			if key != oldpath {
				fs.lock.Unlock()
				return -fuse.ENOTEMPTY
			}
		}
	} else {
		// Check mem for files
		if val, ok := fs.open[oldpath]; ok {
			fs.open[newpath] = val
			delete(fs.open, oldpath)
		}
	}
	fs.lock.Unlock()

	tx.Path = newpath
	writeTxBytes, _ := json.Marshal(tx)
	if len(writeTxBytes) > MaxDiscordFileSize {
		return -fuse.EACCES
	}
	deleteTxBytes, _ := json.Marshal(createDeleteTx(oldpath))
	if len(deleteTxBytes) > MaxDiscordFileSize {
		return -fuse.EACCES
	}

	if len(writeTxBytes)+len(deleteTxBytes) > MaxDiscordFileSize {
		go func() {
			fs.writer.SendTx(writeTxBytes)
			fs.writer.SendTx(deleteTxBytes)
		}()
	} else {
		go func() { fs.writer.SendTx(append(append(writeTxBytes, '\n'), deleteTxBytes...)) }()
	}
	fs.lock.Lock()
	fs.db.Insert(newpath, tx)
	fs.db.Delete(oldpath)
	fs.lock.Unlock()

	return 0
}

func (fs *Dsfs) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	zap.S().Debugw("Getattr", "path", path, "fh", fh)
	fs.lock.Lock()
	defer fs.lock.Unlock()

	// Check open map
	if file, ok := fs.open[path]; ok {
		stat.Mode = fuse.S_IFREG | 0o777
		stat.Size = file.cache.Size()
		stat.Ctim = fuse.NewTimespec(file.ctim)
		stat.Mtim = fuse.NewTimespec(file.mtim)
		return 0
	}

	tx, ok := fs.db.Get(path)
	if !ok {
		return -fuse.ENOENT
	}
	if tx.Type == FileType {
		stat.Mode = fuse.S_IFREG | 0o777
		stat.Size = tx.Size
		stat.Ctim = fuse.NewTimespec(tx.Ctim)
		stat.Mtim = fuse.NewTimespec(tx.Mtim)
	} else {
		stat.Mode = fuse.S_IFDIR | 0o777
	}
	return 0
}

func (fs *Dsfs) Truncate(path string, size int64, fh uint64) int {
	// Please note that Truncate only truncates in mem!
	zap.S().Debugw("Truncate",
		"path", path, "size", size, "fh", fh,
	)
	fs.lock.Lock()

	file, ok := fs.open[path]
	if !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	fs.lock.Unlock()

	file.lock.Lock()
	filesize := file.cache.Size()
	if size == filesize {
		file.lock.Unlock()
		return 0
	} else if size < filesize {
		file.load.truncate(size)
	} else {
		file.load.addRange(filesize, filesize+size)
	}

	file.cache.Truncate(size)
	file.mtim = time.Now()
	file.dirty = true
	file.lock.Unlock()

	return 0
}

func (fs *Dsfs) Read(path string, buff []byte, ofst int64, fh uint64) int {
	zap.S().Debugw("Read",
		"path", path,
		"len(buff)", len(buff),
		"ofst", ofst,
		"fh", fh,
	)
	fs.lock.Lock()

	file, ok := fs.open[path]
	if !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	fs.lock.Unlock()

	buffLen := int64(len(buff))
	var bytesReady int64
	retries := 0

	file.lock.RLock()
	for {
		filesize := file.cache.Size()
		bytesReady = file.load.bytesReady(ofst)
		if bytesReady > 0 {
			if bytesReady > buffLen {
				bytesReady = buffLen
			}
			if ofst+bytesReady > filesize {
				bytesReady = filesize - ofst
			}
			break
		}
		if retries >= MaxRetries {
			file.lock.RUnlock()
			return 0
		}
		retries++
		file.lock.RUnlock()
		time.Sleep(PollInterval)
		file.lock.RLock()
	}

	bytesRead := file.cache.ReadRange(ofst, ofst+bytesReady, buff)
	file.lock.RUnlock()

	return int(bytesRead)
}

func (fs *Dsfs) Write(path string, buff []byte, ofst int64, fh uint64) int {
	// Please note that Write only writes to mem!
	zap.S().Debugw("Write",
		"path", path,
		"len(buff)", len(buff),
		"ofst", ofst,
		"fh", fh,
	)
	fs.lock.Lock()

	file, ok := fs.open[path]
	if !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	fs.lock.Unlock()

	endofst := ofst + int64(len(buff))

	file.lock.Lock()
	filesize := file.cache.Size()
	if endofst > filesize {
		file.cache.Truncate(endofst)
	}

	bytesWrite := file.cache.WriteRange(ofst, endofst, buff)
	if !file.load.isReady(ofst, endofst) {
		file.load.addRange(ofst, endofst)
	}
	file.mtim = time.Now()
	file.dirty = true
	file.lock.Unlock()

	return int(bytesWrite)
}

func (fs *Dsfs) Release(path string, fh uint64) int {
	// All open files are dumped to memory.
	// When a file is closed and is "dirty" (aka modified), the entire file
	// is flushed in the background.
	zap.S().Debugw("Release", "path", path, "fh", fh)
	fs.lock.Lock()

	file, ok := fs.open[path]
	if !ok {
		fs.lock.Unlock()
		return -fuse.ENOENT
	}
	if !file.dirty {
		// TODO: find a good way to evict stale files from mem
		// delete(fs.open, path)
		fs.lock.Unlock()
		return 0
	}

	oldTx, overwrite := fs.db.Get(path)

	tx := &Tx{
		Tx:      WriteTx,
		Path:    path,
		Type:    FileType,
		FileIDs: make([]string, 0),
		Size:    file.cache.Size(),
		Mtim:    file.mtim,
		Ctim:    file.ctim,
	}
	fs.lock.Unlock()

	go func() {
		// Do not attempt to run more than one write job at once for each path
		if file.syncing.Load() {
			return
		}
		zap.S().Debugf("uploading %s in the background", path)
		file.syncing.Store(true)
		defer file.syncing.Store(false)
		defer func() { file.dirty = false }()

		up := func(idx int, fileID string, checksum string) error {
			file.lock.RLock()

			ofst := int64(idx * FileBlockSize)

			// We need to be very careful about this because we want lock with
			// quick and correct contention.
			filesize := file.cache.Size()
			if ofst > filesize {
				file.lock.RUnlock()
				return nil
			}

			end := ofst + FileBlockSize
			if end > filesize {
				end = filesize
			}

			buffer := make([]byte, end-ofst)
			file.cache.ReadRange(ofst, end, buffer)
			newChecksum := sha1.Sum(buffer)
			newChecksumStr := base64.URLEncoding.EncodeToString(newChecksum[:])

			// Checksum valid skipping chunk
			if checksum == newChecksumStr {
				tx.Checksums = append(tx.Checksums, checksum)
				tx.FileIDs = append(tx.FileIDs, fileID)
				file.lock.RUnlock()
				return nil
			}
			file.lock.RUnlock()

			fileID, err := fs.writer.SendData(buffer)
			if err != nil {
				return err
			}
			tx.Checksums = append(tx.Checksums, newChecksumStr)
			tx.FileIDs = append(tx.FileIDs, fileID)
			return nil
		}

		filesize := file.cache.Size()
		end := int(filesize / FileBlockSize)
		if filesize%FileBlockSize != 0 {
			end++
		}

		// Dump data selectively based on checksum
		// or if checksum doesn't exist dump all data
		if overwrite {
			for i := 0; i < end; i++ {
				fileID, checksum := "", ""
				if i < len(oldTx.Checksums) {
					fileID, checksum = oldTx.FileIDs[i], oldTx.Checksums[i]
				}
				err := up(i, fileID, checksum)
				if err != nil {
					return
				}
			}
		} else {
			tx.Checksums = make([]string, 0, end)
			for i := 0; i < end; i++ {
				err := up(i, "", "")
				if err != nil {
					return
				}
			}
		}

		b, _ := json.Marshal(tx)
		if len(b) > MaxDiscordFileSize {
			return
		}
		_, err := fs.writer.SendTx(b)
		if err != nil {
			return
		}
		fs.lock.Lock()
		fs.db.Insert(path, tx)
		fs.lock.Unlock()
		zap.S().Debugw("Release done", "path", path)
	}()

	// TODO: find a good way to evict stale files from mem
	// delete(fs.open, path)
	return 0
}

func (fs *Dsfs) Opendir(path string) (int, uint64) {
	zap.S().Debugw("Opendir", "path", path)
	return 0, 1
}

func (fs *Dsfs) Readdir(
	path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64,
	fh uint64,
) int {
	zap.S().Debugw("Readdir",
		"path", path, "ofst", ofst, "fh", fh,
	)
	fs.lock.Lock()
	defer fs.lock.Unlock()

	fill(".", &fuse.Stat_t{Mode: fuse.S_IFDIR | 0o777}, 0)
	fill("..", nil, 0)
	it := fs.db.Iterator(path)
	for key, tx, ok := it.Next(); ok; key, tx, ok = it.Next() {
		// File is already open
		if _, ok := fs.open[key]; ok {
			continue
		}
		if key == path || getDir(key) != path {
			continue
		}
		name := filepath.Base(key)
		if tx.Type == FileType {
			fill(name, &fuse.Stat_t{
				Mode: fuse.S_IFREG | 0o777,
				Size: tx.Size,
				Ctim: fuse.NewTimespec(tx.Ctim),
				Mtim: fuse.NewTimespec(tx.Mtim),
			}, 0)
		} else {
			fill(name, &fuse.Stat_t{Mode: fuse.S_IFDIR | 0o777}, 0)
		}
	}
	for key, val := range fs.open {
		if getDir(key) != path {
			continue
		}
		name := filepath.Base(key)
		fill(name, &fuse.Stat_t{
			Mode: fuse.S_IFREG | 0o777,
			Size: val.cache.Size(),
			Ctim: fuse.NewTimespec(val.ctim),
			Mtim: fuse.NewTimespec(val.mtim),
		}, 0)
	}
	return 0
}

func (fs *Dsfs) Releasedir(path string, fh uint64) int {
	zap.S().Debugw("Releasedir", "path", path, "fh", fh)
	return 0
}

func (fs *Dsfs) Statfs(path string, stat *fuse.Statfs_t) int {
	zap.S().Debugw("Statfs", "path", path)

	// We are injecting some phony data here.
	// None of this should matter.
	stat.Bsize = 4096
	stat.Frsize = stat.Bsize
	stat.Blocks = 256 * 1024 * 1024
	stat.Bfree = stat.Blocks
	stat.Bavail = stat.Blocks
	return 0
}

func (fs *Dsfs) ApplyLiveTx(path string, tx *Tx) error {
	zap.S().Debugw("ApplyLiveTx", "tx.Path", tx.Path)
	fs.lock.Lock()

	fs.db.Insert(path, tx)
	file, ok := fs.open[tx.Path]
	if !ok {
		fs.lock.Unlock()
		return nil
	}
	fs.lock.Unlock()

	file.lock.Lock()
	filesize := file.cache.Size()
	if tx.Size < filesize {
		file.load.truncate(tx.Size)
	}
	file.cache.Truncate(tx.Size)
	file.lock.Unlock()

	buffer := make([]byte, FileBlockSize)
	for idx, checksum := range tx.Checksums {
		file.lock.Lock()
		ofst := int64(idx * FileBlockSize)
		// Something can happen between truncating and patching memory.
		// In this case it's really hard to recover.
		filesize = file.cache.Size()
		if ofst >= filesize {
			file.lock.Unlock()
			return errors.New("file changed while upcoming change is applied")
		}
		end := ofst + FileBlockSize
		if end > filesize {
			end = filesize
		}

		buffer = make([]byte, end-ofst)
		file.cache.ReadRange(ofst, end, buffer)
		oldChecksum := sha1.Sum(buffer)
		oldChecksumStr := base64.URLEncoding.EncodeToString(oldChecksum[:])
		file.lock.Unlock()

		if checksum == oldChecksumStr {
			continue
		}

		n, err := getDataFile(fs.dataChannel.ID, tx.FileIDs[idx], buffer)
		if err != nil {
			return err
		}

		file.lock.Lock()
		ofstn := ofst + int64(n)
		file.cache.WriteRange(ofst, ofstn, buffer)
		if !file.load.isReady(ofst, ofstn) {
			file.load.addRange(ofst, ofstn)
		}
		file.lock.Unlock()
	}

	return nil
}

func (fs *Dsfs) GetNewCache() Cache {
	switch fs.cacheType {
	case "disk":
		return NewDiskCache()
	case "memory":
		return NewMemoryCache()
	default:
		panic("unknown cache type")
	}
}
