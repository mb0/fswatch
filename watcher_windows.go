// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows

package fswatch

// http://msdn.microsoft.com/en-us/library/aa365465%28VS.85%29.aspx

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createFlags = syscall.FILE_NOTIFY_CHANGE_FILE_NAME | syscall.FILE_NOTIFY_CHANGE_DIR_NAME
	modifyFlags = syscall.FILE_NOTIFY_CHANGE_LAST_WRITE | syscall.FILE_NOTIFY_CHANGE_SIZE
	allFlags    = createFlags | modifyFlags
)

const errMoreData syscall.Errno = 234

type watch struct {
	overlap syscall.Overlapped
	handle  syscall.Handle
	mask    uint32
	info    *info
	buf     [4096]byte
}

type watcher struct {
	mutex   sync.RWMutex
	port    syscall.Handle
	context Context
	tree    *tree
	signal  chan func() (done bool)
}

func newwatcher(ctx *Context) (*watcher, error) {
	port, err := syscall.CreateIoCompletionPort(syscall.InvalidHandle, 0, 0, 1)
	if err != nil {
		return nil, os.NewSyscallError("CreateIoCompletionPort", err)
	}
	w := &watcher{
		port:    port,
		context: defaults(ctx),
		tree:    new(tree),
		signal:  make(chan func() bool, 1),
	}
	go w.run(port)
	return w, nil
}

func watchFilter(nfo *info) bool {
	return nfo.mode&os.ModeDir != 0
}

func (w *watcher) load(path string, recursive bool) error {
	w.mutex.RLock()
	port := w.port
	w.mutex.RUnlock()
	if port == syscall.InvalidHandle {
		return ErrClosed
	}
	resp := make(chan error)
	flags := uint(explicit)
	if recursive {
		flags |= recurse
	}
	w.signal <- func() bool {
		resp <- w.loadImpl(path, flags, 0, allFlags, allFlags)
		return false
	}
	err := syscall.PostQueuedCompletionStatus(w.port, 0, 0, nil)
	if err != nil {
		return os.NewSyscallError("PostQueuedCompletionStatus", err)
	}

	err = <-resp
	if err == SkipDir {
		return nil
	}
	return err
}

func (w *watcher) watch(nfo *info, flags uint32) error {
	resp := make(chan error)
	w.signal <- func() bool {
		resp <- w.add(nfo, allFlags)
		return false
	}
	err := syscall.PostQueuedCompletionStatus(w.port, 0, 0, nil)
	if err != nil {
		return os.NewSyscallError("PostQueuedCompletionStatus", err)
	}
	return <-resp
}

func (w *watcher) add(nfo *info, flags uint32) error {
	handle, err := syscall.CreateFile(syscall.StringToUTF16Ptr(nfo.path), syscall.FILE_LIST_DIRECTORY,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return os.NewSyscallError("CreateFile", err)
	}
	_, err = syscall.CreateIoCompletionPort(handle, w.port, 0, 1)
	if err != nil {
		syscall.CloseHandle(handle)
		return os.NewSyscallError("CreateIoCompletionPort", err)
	}
	nfo.watch = &watch{handle: handle, mask: flags, info: nfo}
	return w.start(nfo)
}

func (w *watcher) unload(path string, recursive bool) error {
	w.mutex.RLock()
	port := w.port
	nfo := w.tree.get(path)
	w.mutex.RUnlock()
	if port == syscall.InvalidHandle {
		return ErrClosed
	}
	if nfo == nil || nfo.watch == nil {
		return nil
	}
	resp := make(chan error)
	w.signal <- func() bool {
		w.mutex.Lock()
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
		resp <- nil
		return false
	}
	err := syscall.PostQueuedCompletionStatus(port, 0, 0, nil)
	if err != nil {
		return os.NewSyscallError("PostQueuedCompletionStatus", err)
	}
	err = <-resp
	return err
}

func (w *watcher) rm(nfo *info) error {
	err := syscall.CancelIo(nfo.watch.handle)
	if err != nil {
		return os.NewSyscallError("CancelIo", err)
	}
	err = syscall.CloseHandle(nfo.watch.handle)
	if err != nil {
		return os.NewSyscallError("CloseHandle", err)
	}
	nfo.watch.info = nil
	nfo.watch = nil
	return nil
}

func (w *watcher) close() error {
	w.mutex.RLock()
	port := w.port
	w.mutex.RUnlock()
	if port == syscall.InvalidHandle {
		return ErrClosed
	}
	w.signal <- func() bool {
		w.mutex.Lock()
		defer w.mutex.Unlock()
		w.tree.deleteAll("", func(nfo *info) {
			if nfo.watch == nil {
				return
			}
			if err := w.rm(nfo); err != nil {
				w.context.Error(err)
			}
		})
		err := syscall.CloseHandle(port)
		if err != nil {
			w.context.Error(os.NewSyscallError("CloseHandle", err))
		}
		return true
	}
	err := syscall.PostQueuedCompletionStatus(port, 0, 0, nil)
	if err != nil {
		return os.NewSyscallError("PostQueuedCompletionStatus", err)
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.port = syscall.InvalidHandle
	return nil
}

func (w *watcher) start(nfo *info) error {
	watch := nfo.watch
	err := syscall.CancelIo(watch.handle)
	if err != nil {
		return os.NewSyscallError("CancelIo", err)
	}
	err = syscall.ReadDirectoryChanges(watch.handle, &watch.buf[0], uint32(len(watch.buf)), false, watch.mask, nil, &watch.overlap, 0)
	if err != nil {
		if err == syscall.ERROR_ACCESS_DENIED {
			var list []*info
			w.mutex.Lock()
			w.tree.deleteAll(nfo.path, func(nfo *info) {
				if nfo.watch == nil {
					return
				}
				if err := w.rm(nfo); err != nil {
					w.context.Error(err)
				}
				list = append(list, nfo)
			})
			w.mutex.Unlock()
			for _, nfo = range list {
				w.context.Handle(Delete, nfo)
			}
			return nil
		}
		return os.NewSyscallError("ReadDirectoryChanges", err)
	}
	return nil
}

type qitem struct {
	action uint32
	info   *info
	name   string
}

func (w *watcher) run(port syscall.Handle) {
	runtime.LockOSThread()
	var n, key uint32
	var overlap *syscall.Overlapped
	var queue []qitem
	var timeout uint32
	for {
		timeout = syscall.INFINITE
		if len(queue) > 0 {
			timeout = 10
		}
		err := syscall.GetQueuedCompletionStatus(port, &n, &key, &overlap, timeout)
		watch := (*watch)(unsafe.Pointer(overlap))
		if watch == nil {
			select {
			case sig := <-w.signal:
				if done := sig(); done {
					return
				}
			default:
				for _, q := range queue {
					w.handle(q.action, q.info, q.name)
				}
				queue = queue[:0]
			}
			continue
		}
		switch err {
		case nil:
		case errMoreData:
			n = uint32(len(watch.buf))
		case syscall.ERROR_OPERATION_ABORTED:
			continue
		case syscall.ERROR_ACCESS_DENIED:
			var list []*info
			w.mutex.Lock()
			w.tree.deleteAll(watch.info.path, func(nfo *info) {
				if nfo.watch == nil {
					return
				}
				if err := w.rm(nfo); err != nil {
					w.context.Error(err)
				}
				list = append(list, nfo)
			})
			w.mutex.Unlock()
			for _, nfo := range list {
				w.context.Handle(Delete, nfo)
			}
			continue
		default:
			w.context.Error(os.NewSyscallError("GetQueuedCompletionStatus", err))
			continue
		}
		if n <= 0 {
			w.context.Error(errShortRead)
		}
		queued := len(queue)
		for offset := uint32(0); offset < n-16; {
			raw := (*syscall.FileNotifyInformation)(unsafe.Pointer(&watch.buf[offset]))
			fnb := (*[syscall.MAX_PATH]uint16)(unsafe.Pointer(&raw.FileName))[:raw.FileNameLength/2]
			name := syscall.UTF16ToString(fnb)
			found := false
			for _, q := range queue {
				if q.info == watch.info && q.name == name {
					found = !isDelete(q.action) && !isDelete(raw.Action)
					break
				}
			}
			if !found {
				queue = append(queue, qitem{raw.Action, watch.info, name})
			}
			if raw.NextEntryOffset == 0 {
				break
			}
			offset += raw.NextEntryOffset
			if offset > n {
				w.context.Error(ErrOverflow)
			}
		}
		for _, q := range queue[:queued] {
			w.handle(q.action, q.info, q.name)
		}
		copy(queue, queue[queued:])
		queue = queue[:len(queue)-queued]
		err = w.start(watch.info)
		if err != nil {
			w.context.Error(err)
		}
	}
}

func isDelete(action uint32) bool {
	return action == syscall.FILE_ACTION_REMOVED || action == syscall.FILE_ACTION_RENAMED_OLD_NAME
}

func (w *watcher) handle(action uint32, nfo *info, name string) {
	path, fi := nfo.path, nfo
	if name != "" {
		path = filepath.Join(path, name)
		fi = nil
	}
	if isDelete(action) {
		var list []*info
		w.mutex.Lock()
		w.tree.deleteAll(path, func(fi *info) {
			if fi.watch != nil {
				fi.watch.info = nil
				fi.watch = nil
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
