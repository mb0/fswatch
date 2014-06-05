// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fswatch

// http://man7.org/linux/man-pages/man7/inotify.7.html

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createFlags = syscall.IN_CREATE | syscall.IN_MOVED_TO
	modifyFlags = syscall.IN_CLOSE_WRITE | syscall.IN_ATTRIB
	deleteFlags = syscall.IN_MOVED_FROM | syscall.IN_DELETE | syscall.IN_DELETE_SELF
	allFlags    = createFlags | modifyFlags | deleteFlags ^ syscall.IN_DELETE_SELF | syscall.IN_EXCL_UNLINK
)

type watch struct {
	fd int
}

type watcher struct {
	mutex   sync.RWMutex
	fd      int
	context Context
	tree    *tree
	fdmap   map[int]*info
	signal  chan func() (done bool)
}

func newwatcher(ctx *Context) (*watcher, error) {
	fd, err := syscall.InotifyInit()
	if fd == -1 {
		return nil, os.NewSyscallError("InotifyInit", err)
	}
	w := &watcher{
		fd:      fd,
		context: defaults(ctx),
		tree:    new(tree),
		fdmap:   make(map[int]*info),
		signal:  make(chan func() bool, 1),
	}
	go w.run(fd)
	return w, nil
}

func watchFilter(info *info) bool {
	return info.mode&os.ModeDir != 0
}

func (w *watcher) hasParentWatch(path string) bool {
	if path, _ = filepath.Split(path); path[len(path)-1] == os.PathSeparator {
		path = path[:len(path)-1]
	}
	return w.tree.get(path) != nil
}

func (w *watcher) load(path string, recursive bool) error {
	rootFlags := uint32(allFlags)
	w.mutex.RLock()
	fd := w.fd
	if !w.hasParentWatch(path) {
		rootFlags |= syscall.IN_DELETE_SELF
	}
	w.mutex.RUnlock()
	if fd == -1 {
		return ErrClosed
	}
	fiFlags := uint(explicit)
	if recursive {
		fiFlags |= recurse
	}
	err := w.loadImpl(path, fiFlags, 0, rootFlags, allFlags)
	if err == SkipDir {
		return nil
	}
	return err
}

func (w *watcher) add(info *info, flags uint32) error {
	fd, err := syscall.InotifyAddWatch(w.fd, info.path, flags)
	if fd == -1 {
		return os.NewSyscallError("InotifyAddWatch", err)
	}
	info.watch = &watch{fd: fd}
	w.fdmap[fd] = info
	return nil
}

func (w *watcher) unload(path string, recursive bool) error {
	w.mutex.RLock()
	fd := w.fd
	nfo := w.tree.get(path)
	w.mutex.RUnlock()
	if fd == -1 {
		return ErrClosed
	}
	if nfo == nil || nfo.watch == nil {
		return nil
	}
	w.mutex.Lock()
	var err error
	if nfo.watch != nil {
		err = w.rm(nfo)
		nfo.watch = nil
	}
	var reload []*info
	w.tree.deleteAll(nfo.path, func(nfo *info) {
		if !recursive && nfo.flags&explicit != 0 && nfo.path != path {
			reload = append(reload, nfo)
		}
		if nfo.watch != nil {
			if err := w.rm(nfo); err != nil {
				w.context.Error(err)
			}
		}
	})
	w.mutex.Unlock()
	for _, nfo = range reload {
		err := w.loadImpl(nfo.path, nfo.flags&(recurse|explicit), 0, allFlags, allFlags)
		if err != nil {
			w.context.Error(err)
		}
	}
	return err
}

func (w *watcher) rm(nfo *info) error {
	code, err := syscall.InotifyRmWatch(w.fd, uint32(nfo.watch.fd))
	if code == -1 {
		return os.NewSyscallError("InotifyRmWatch", err)
	}
	delete(w.fdmap, nfo.watch.fd)
	return nil
}

func (w *watcher) close() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.fd == -1 {
		return ErrClosed
	}
	if w.tree.root == nil {
		fd, err := syscall.InotifyAddWatch(w.fd, "/", syscall.IN_DELETE_SELF)
		if fd == -1 {
			return os.NewSyscallError("InotifyAddWatch", err)
		}
		w.fdmap[fd] = &info{path: "/", watch: &watch{fd}}
	}
	w.signal <- func() bool {
		w.mutex.Lock()
		defer w.mutex.Unlock()
		err := syscall.Close(w.fd)
		if err != nil {
			w.context.Error(os.NewSyscallError("Close", err))
		}
		w.fd, w.fdmap = -1, nil
		return true
	}
	for _, info := range w.fdmap {
		err := w.rm(info)
		if err != nil {
			w.context.Error(err)
		}
	}
	return nil
}

func (w *watcher) run(fd int) {
	var buf [syscall.SizeofInotifyEvent * 4096]byte
	for {
		n, err := syscall.Read(fd, buf[:])
		if n == 0 {
			err := w.close()
			if err != nil {
				w.context.Error(err)
			}
			return
		} else if n < syscall.SizeofInotifyEvent {
			if err != nil {
				w.context.Error(os.NewSyscallError("Read", err))
			} else {
				w.context.Error(errShortRead)
			}
			continue
		}
		select {
		case done := <-w.signal:
			if done() {
				return
			}
		default:
		}
		offset := 0
		for offset <= n-syscall.SizeofInotifyEvent {
			raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[offset]))
			w.mutex.RLock()
			info := w.fdmap[int(raw.Wd)]
			w.mutex.RUnlock()
			if info != nil {
				var name string
				if raw.Len > 0 {
					start := &buf[offset+syscall.SizeofInotifyEvent]
					bytes := *(*[syscall.PathMax]byte)(unsafe.Pointer(start))
					name = strings.TrimRight(string(bytes[:raw.Len]), "\000")
				}
				w.handle(raw.Mask, info, name)
			}
			offset += syscall.SizeofInotifyEvent + int(raw.Len)
		}
	}
}

func (w *watcher) handle(mask uint32, nfo *info, name string) {
	path, fi := nfo.path, nfo
	if name != "" {
		path = filepath.Join(path, name)
		fi = nil
	}
	if mask&(deleteFlags|syscall.IN_IGNORED) != 0 {
		var list []*info
		w.mutex.Lock()
		w.tree.deleteAll(path, func(fi *info) {
			if fi.watch != nil {
				delete(w.fdmap, fi.watch.fd)
			}
			list = append(list, fi)
		})
		w.mutex.Unlock()
		for _, fi = range list {
			w.context.Handle(Delete, fi)
		}
		return
	}
	if fi == nil {
		w.mutex.RLock()
		fi = w.tree.get(path)
		w.mutex.RUnlock()
	}
	if fi == nil {
		err := w.loadImpl(path, nfo.flags&recurse, Create, allFlags, allFlags)
		if err != nil && err != SkipDir {
			if !os.IsNotExist(err) {
				w.context.Error(err)
			}
		}
	} else {
		nfi, err := os.Lstat(path)
		if err != nil {
			if !os.IsNotExist(err) {
				w.context.Error(err)
			}
			return
		}
		fi.update(nfi)
		w.context.Handle(Modify, fi)
	}
}
