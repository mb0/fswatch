// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build freebsd openbsd netbsd darwin

package fswatch

// http://www.freebsd.org/cgi/man.cgi?query=kqueue

import (
	"fmt"
	"os"
	"sync"
	"syscall"
)

const (
	modifyFlags = syscall.NOTE_WRITE | syscall.NOTE_EXTEND | syscall.NOTE_ATTRIB
	deleteFlags = syscall.NOTE_DELETE | syscall.NOTE_RENAME | syscall.NOTE_REVOKE
	allFlags    = modifyFlags | deleteFlags
)

var openwdFlags = syscall.O_NONBLOCK | syscall.O_RDONLY

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
	fd, err := syscall.Kqueue()
	if fd == -1 {
		return nil, os.NewSyscallError("Kqueue", err)
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

func watchFilter(nfo *info) bool {
	return true
}

func (w *watcher) load(path string, recursive bool) error {
	w.mutex.RLock()
	fd := w.fd
	w.mutex.RUnlock()
	if fd == -1 {
		return ErrClosed
	}
	fiFlags := uint(explicit)
	if recursive {
		fiFlags |= recurse
	}
	err := w.loadImpl(path, fiFlags, 0, allFlags, allFlags)
	if err == SkipDir {
		return nil
	}
	return err
}

func (w *watcher) add(nfo *info, flags uint32) error {
	fd, err := syscall.Open(nfo.path, openwdFlags, 0700)
	if fd == -1 {
		return err
	}
	ev := []syscall.Kevent_t{{Fflags: flags}}
	syscall.SetKevent(&ev[0], fd, syscall.EVFILT_VNODE, syscall.EV_ADD|syscall.EV_CLEAR)
	code, err := syscall.Kevent(w.fd, ev, nil, nil)
	if code == -1 {
		return os.NewSyscallError("Kevent", err)
	}
	nfo.watch = &watch{fd: fd}
	w.fdmap[fd] = nfo
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
		} else if nfo.watch != nil {
			if err := w.rm(nfo); err != nil {
				w.context.Error(err)
			}
		}
	})
	for _, nfo = range reload {
		w.tree.insert(nfo)
	}
	w.mutex.Unlock()
	return err
}

func (w *watcher) rm(nfo *info) error {
	err := syscall.Close(nfo.watch.fd)
	if err != nil {
		return os.NewSyscallError("Close rm", err)
	}
	delete(w.fdmap, nfo.watch.fd)
	return nil
}

func (w *watcher) close() error {
	w.mutex.RLock()
	fd := w.fd
	w.mutex.RUnlock()
	if fd == -1 {
		return ErrClosed
	}
	w.signal <- func() bool {
		w.mutex.Lock()
		defer w.mutex.Unlock()
		err := syscall.Close(fd)
		if err != nil {
			w.context.Error(os.NewSyscallError("Close close", err))
		}
		w.fdmap = nil
		return true
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()
	for _, nfo := range w.fdmap {
		err := w.rm(nfo)
		if err != nil {
			w.context.Error(err)
		}
	}
	w.fd = -1
	return nil
}

func (w *watcher) run(fd int) {
	var buf [1024]syscall.Kevent_t
	wait := syscall.NsecToTimespec(50e6)
	for {
		n, err := syscall.Kevent(fd, nil, buf[:], &wait)
		select {
		case done := <-w.signal:
			if done() {
				return
			}
		default:
		}
		if err != nil {
			if err != syscall.EINTR {
				w.context.Error(os.NewSyscallError("Kevent", err))
			}
			continue
		}
		for _, ev := range buf[:n] {
			w.mutex.Lock()
			nfo := w.fdmap[int(ev.Ident)]
			w.mutex.Unlock()
			if nfo == nil || nfo.watch == nil {
				w.context.Error(fmt.Errorf("unknown watch"))
				continue
			}
			w.handle(ev.Fflags, nfo)
		}
	}
}

func (w *watcher) handle(mask uint32, nfo *info) {
	path, fi := nfo.path, nfo
	if mask&deleteFlags != 0 {
		var list []*info
		w.mutex.Lock()
		w.tree.deleteAll(nfo.path, func(fi *info) {
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
	if nfo.IsDir() && mask&modifyFlags != 0 {
		err := w.loadImpl(path, fi.flags&recurse, Create, allFlags, allFlags)
		if err != nil && err != SkipDir {
			if !os.IsNotExist(err) {
				w.context.Error(err)
			}
		}
	} else {
		nfi, err := os.Lstat(nfo.path)
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
