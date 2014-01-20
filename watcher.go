// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fswatch handles file change notifications and caches file informations.
package fswatch

import (
	"os"
	"path/filepath"
)

// Context holds a filter and handler functions for file events and errors
type Context struct {
	// Handle handles file events
	Handle func(Event, FileInfo)
	// Filter returns `false` if the watcher should ignore FileInfo
	Filter func(FileInfo) bool
	// Error handles errors
	Error func(error)
}

// FileInfo is an `os.FileInfo` with additional information
type FileInfo interface {
	os.FileInfo
	// Path returns the absolute path of the file
	Path() string
	// Ignored returns whether this file was ignored by `Context.Filter`
	Ignored() bool
}

// Watcher caches file informations and watches them for changes.
type Watcher struct {
	*watcher
}

// New creates and initializes a new watcher
func New(ctx *Context) (Watcher, error) {
	w, err := newwatcher(ctx)
	return Watcher{w}, err
}

// Load starts watching the directory at `path`
// and all descendent directories if recursive is `true`
func (w Watcher) Load(path string, recursive bool) error {
	path = filepath.Clean(path)
	return w.load(path, recursive)
}

// Get returns a cached `FileInfo` at `path` or `nil`
// Get ignores files previously filtered out by `Context.Filter`.
func (w Watcher) Get(path string) FileInfo {
	path = filepath.Clean(path)
	w.mutex.RLock()
	fi := w.tree.get(path)
	w.mutex.RUnlock()
	if fi == nil || fi.Ignored() {
		return nil
	}
	return fi
}

// Lstat mimics `os.Lstat` and returns a cached `FileInfo` at `path` or an `os.PathError`.
// Lstat ignores files previously filtered out by `Context.Filter`.
func (w Watcher) Lstat(path string) (os.FileInfo, error) {
	if info := w.Get(path); info != nil {
		return info, nil
	}
	return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
}

// Traverse will call `travFn` with cached `FileInfo`s at root and its descendents.
// Traverse ignores files previously filtered out by `Context.Filter`.
// The passed in function can return `SkipDir` to skip the current directory.
func (w Watcher) Traverse(root string, travFn func(FileInfo) error) error {
	root = filepath.Clean(root)
	w.mutex.RLock()
	defer w.mutex.RUnlock()
	return w.tree.walk(root, travFn)
}

// Walk mimics `filepath.Walk` and calls `walkFn` with cached `os.FileInfo`s at root and its descendents.
// Walk ignores files previously filtered out by `Context.Filter`.
// The passed in function can return `SkipDir` to skip the current directory.
func (w Watcher) Walk(root string, walkFn filepath.WalkFunc) error {
	var found bool
	err := w.Traverse(root, func(info FileInfo) error {
		found = true
		return walkFn(info.Path(), info, nil)
	})
	if !found {
		return walkFn(root, nil, err)
	}
	return err
}

// Unload stops watching the directory at `path`
// and all descendent directories if recursive is `true`
func (w Watcher) Unload(path string, recursive bool) error {
	path = filepath.Clean(path)
	return w.unload(path, recursive)
}

// Close will close the watcher and release the underlying resources
func (w Watcher) Close() error {
	return w.close()
}
