// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fswatch

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	ignored = 1 << iota
	explicit
	recurse
)

type info struct {
	watch *watch
	mutex sync.RWMutex
	path  string
	mode  os.FileMode
	modt  time.Time
	size  int64
	flags uint
}

func newInfo(path string, fi os.FileInfo) *info {
	return &info{
		path: path,
		mode: fi.Mode(),
		modt: fi.ModTime(),
		size: fi.Size(),
	}
}

func (i *info) Path() string {
	return i.path
}

func (i *info) Name() string {
	return filepath.Base(i.path)
}

func (i *info) Sys() interface{} {
	return nil
}

func (i *info) Size() int64 {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.size
}

func (i *info) Mode() os.FileMode {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.mode
}

func (i *info) ModTime() time.Time {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.modt
}

func (i *info) IsDir() bool {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.mode&os.ModeDir != 0
}

func (i *info) Ignored() bool {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.flags&ignored != 0
}

func (i *info) update(fi os.FileInfo) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.mode = fi.Mode()
	i.modt = fi.ModTime()
	i.size = fi.Size()
}
