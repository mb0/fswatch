// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fswatch

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWatch(t *testing.T) {
	// setup test environment
	env := newtestenv(t)
	defer env.close()
	// create
	file1 := env.createWriteClose(env.root, "file1")
	time.Sleep(waitfor)
	// remove
	env.remove(file1)
	time.Sleep(waitfor)
	// recreate
	env.createWriteClose(file1)
	time.Sleep(waitfor)
	// change
	env.openWriteClose(file1)
	time.Sleep(waitfor)
	// remove again
	env.remove(file1)
	time.Sleep(waitfor)
	// remove root watch and dir
	env.unload(env.root, false)
	os.RemoveAll(env.root)
	time.Sleep(waitfor)
	// close watcher
	env.watcher.close()
	time.Sleep(waitfor)
	// check results
	env.check()
}

func TestRename(t *testing.T) {
	// setup test environment
	env := newtestenv(t)
	defer env.close()
	// create
	dir := env.mkdir(env.root, "foo")
	file := env.createWriteClose(dir, "file")
	time.Sleep(waitfor)
	// rename
	newdir := filepath.Join(env.root, "bar")
	err := os.Rename(dir, newdir)
	if err != nil {
		t.Fatal("failed to rename.", err)
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "windows" {
		env.expect = append(env.expect,
			record{Delete, dir, false},
			record{Delete, file, false},
			record{Create, newdir, false},
			record{Create, filepath.Join(newdir, "file"), false},
		)
	} else {
		env.expect = append(env.expect,
			record{Create, newdir, false},
			record{Create, filepath.Join(newdir, "file"), false},
			record{Delete, dir, false},
			record{Delete, file, false},
		)
	}
	time.Sleep(waitfor)
	// close and check results
	env.watcher.close()
	time.Sleep(waitfor)
	env.check()
}

func TestWatchDirs(t *testing.T) {
	// setup test environment
	env := newtestenv(t)
	defer env.close()
	time.Sleep(waitfor)
	// create new directory
	dir1 := env.mkdir(env.root, "dir1")
	time.Sleep(waitfor)
	dir2 := env.mkdir(dir1, "dir2")
	time.Sleep(waitfor)
	// remove dirs
	env.remove(dir2)
	time.Sleep(waitfor)
	env.remove(dir1)
	// check results
	time.Sleep(waitfor)
	env.check()
}

func TestWatchOne(t *testing.T) {
	// setup test environment
	env := newtestenv(t)
	defer env.close()
	// create files
	dir1 := env.mkdir(env.root, "dir1")
	dir2 := env.mkdir(env.root, "dir2")
	time.Sleep(waitfor)
	env.watcher.load(dir1, true)
	env.watcher.load(dir2, false)
	time.Sleep(waitfor)
	// unload root watch
	env.unload(env.root, false)
	time.Sleep(waitfor)
	// write to files and remove
	file1 := env.createWriteClose(dir1, "file1")
	time.Sleep(waitfor)
	file2 := env.createWriteClose(dir2, "file2")
	time.Sleep(waitfor)
	env.remove(file1)
	env.remove(file2)
	time.Sleep(waitfor)
	env.remove(dir1)
	env.remove(dir2)
	// check results
	time.Sleep(waitfor)
	env.check()
}

func TestClose(t *testing.T) {
	// setup test environment
	env := newtestenv(t)
	defer env.close()
	// remove root watch to test closing an empty watcher
	env.unload(env.root, false)
	// close watcher
	start := time.Now()
	err := env.watcher.close()
	if err != nil {
		t.Fatal("failed to close watcher", err)
	}
	// the watcher must not block on close
	if time.Now().Sub(start) > time.Millisecond {
		t.Fatal("close should not block")
	}
	// the watcher must close in time
	time.Sleep(waitfor)
	err = env.watcher.close()
	if err != ErrClosed {
		t.Fatal("expected closed watcher", err)
	}
}
