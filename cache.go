package main

import (
	"io"
	"os"

	"go.skia.org/infra/go/util/limitwriter"
)

// Cache provides an interface to cache a file
type Cache interface {
	ReadRange(start int64, end int64, buffer []byte) int64
	WriteRange(start int64, end int64, buffer []byte) int64
	Truncate(size int64)
	Size() int64
	Rm()
}

// MemoryCache is a cache that stores data in memory
type MemoryCache struct {
	data []byte
}

// NewMemoryCache creates a new MemoryCache
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{data: make([]byte, 0)}
}

// ReadRange reads a range of bytes from the cache
func (c *MemoryCache) ReadRange(start int64, end int64, buffer []byte) int64 {
	return int64(copy(buffer, c.data[start:end]))
}

// WriteRange writes a range of bytes to the cache
func (c *MemoryCache) WriteRange(start int64, end int64, buffer []byte) int64 {
	return int64(copy(c.data[start:end], buffer))
}

// Truncate truncates the cache to size
func (c *MemoryCache) Truncate(size int64) {
	if c.Size() < size {
		c.data = append(c.data, make([]byte, size-c.Size())...)
	} else {
		c.data = c.data[:size]
	}
}

// Size returns the size of the data
func (c *MemoryCache) Size() int64 {
	return int64(len(c.data))
}

// Rm removes the cache
func (c *MemoryCache) Rm() {
	c.data = nil
}

// DiskCache is a cache that stores data in disk
type DiskCache struct {
	file *os.File
	size int64
}

// NewDiskCache creates a new DiskCache
func NewDiskCache() *DiskCache {
	file, _ := os.CreateTemp("", "dsfs")
	return &DiskCache{file: file, size: 0}
}

// ReadRange reads a range of bytes from the cache
func (c *DiskCache) ReadRange(start int64, end int64, buffer []byte) int64 {
	c.file.Seek(start, io.SeekStart)
	n, _ := io.LimitReader(c.file, end-start).Read(buffer)
	return int64(n)
}

// WriteRange writes a range of bytes to the cache
func (c *DiskCache) WriteRange(start int64, end int64, buffer []byte) int64 {
	c.file.Seek(start, io.SeekStart)
	write, _ := limitwriter.New(c.file, int(end-start)).Write(buffer)
	return int64(write)
}

// Truncate truncates the cache to size
func (c *DiskCache) Truncate(size int64) {
	c.file.Truncate(size)
	c.size = size
}

// Size returns the size of the data
func (c *DiskCache) Size() int64 {
	return c.size
}

// Rm removes the cache
func (c *DiskCache) Rm() {
	c.file.Close()
	os.Remove(c.file.Name())
}
