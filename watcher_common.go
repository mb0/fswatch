// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fswatch

import (
	"errors"
	"log"
	"os"
	"path/filepath"
)

// Create, Modify and Delete are all possible events
// that can be received by `Context.Handle`
const (
	Create Event = 1 << iota
	Modify
	Delete
)

// ErrClosed is returned if the watcher cannot take action because it is closed.
var ErrClosed = errors.New("watcher was already closed")

// ErrNotDir is used to indicate that the watcher cannot load a path because it is not directory.
var ErrNotDir = errors.New("can only watch directories")

// ErrOverflow is used to indicated that the watcher may have missed any number of file events.
var ErrOverflow = errors.New("watcher overflow")

// SkipDir is the same as `filepath.SkipDir` and used as a return value from the functions passed to
// Walk or Traverse to indicate that the directory named in the call is to be skipped.
var SkipDir = filepath.SkipDir

var errShortRead = errors.New("short read")

// Event is either Create, Modify or Delete
type Event uint

func (e Event) String() string {
	switch e {
	case Create:
		return "Create"
	case Modify:
		return "Modify"
	case Delete:
		return "Delete"
	}
	return "Unknown"
}

func defaults(ctx *Context) Context {
	var c Context
	if ctx != nil {
		c = *ctx
	}
	if c.Handle == nil {
		c.Handle = func(Event, FileInfo) {}
	}
	if c.Filter == nil {
		c.Filter = func(FileInfo) bool { return true }
	}
	if c.Error == nil {
		c.Error = func(err error) { log.Println(err) }
	}
	return c
}

func (w *watcher) loadImpl(root string, flags uint, event Event, rootflags, otherflags uint32) error {
	fi, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !fi.IsDir() && flags&explicit != 0 {
		return ErrNotDir
	}
	f := newInfo(root, fi)
	if !w.context.Filter(f) {
		return nil
	}
	f.flags |= flags
	w.mutex.Lock()
	dup := w.tree.insert(f)
	w.mutex.Unlock()
	if dup != nil {
		dup.mutex.Lock()
		dup.flags |= f.flags
		dup.mutex.Unlock()
		// TODO(mb0) check if changed
		//return nil
		f = dup
	} else if watchFilter(f) {
		w.mutex.Lock()
		err = w.add(f, rootflags)
		w.mutex.Unlock()
		if err != nil {
			if !os.IsNotExist(err) {
				w.context.Error(err)
			}
		}
	}
	var list []*info
	walker := filepath.WalkFunc(func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			if !os.IsNotExist(err) {
				w.context.Error(err)
			}
			return nil
		}
		if path == root {
			return nil
		}
		f := newInfo(path, fi)
		ignore := !w.context.Filter(f)
		w.mutex.Lock()
		defer w.mutex.Unlock()
		if w.tree.insert(f) != nil {
			// TODO(mb0) check if changed
			return SkipDir
		}
		if ignore {
			f.flags |= ignored
			if fi.IsDir() {
				return SkipDir
			}
			return nil
		}
		if watchFilter(f) {
			err = w.add(f, otherflags)
			if err != nil {
				if !os.IsNotExist(err) {
					w.context.Error(err)
				}
			}
		}
		if event != 0 {
			list = append(list, f)
		}
		if fi.IsDir() && flags&recurse == 0 {
			return SkipDir
		}
		return nil
	})
	err = filepath.Walk(root, walker)
	if event != 0 {
		if dup == nil {
			w.context.Handle(event, f)
		}
		for _, f = range list {
			w.context.Handle(event, f)
		}
	}
	return err
}
