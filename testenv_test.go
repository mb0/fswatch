// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fswatch

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var waitfor = 15 * time.Millisecond

// record represents an event received by a `fswatch.Handler`.
type record struct {
	Event
	path     string
	optional bool
}

func (r record) String() string {
	return fmt.Sprintf("%s %q", r.Event, r.path)
}

// recorder holds recorded events and errors
type recorder struct {
	sync.Mutex
	events []record
	errors []error
}

// testenv represents a test environment for watcher tests.
// it provides utility methods to modify files and check recorded events and errors.
type testenv struct {
	watcher *watcher
	*testing.T
	root   string
	expect []record
	recorder
}

// newtestenv sets up a watcher for a temporary folder
func newtestenv(t *testing.T) *testenv {
	root, err := ioutil.TempDir("", "watchfs")
	if err != nil {
		t.Fatal("failed to setup test environment", err)
	}
	env := &testenv{T: t, root: root}
	w, err := newwatcher(&Context{
		Handle: env.handle,
		Error:  env.error,
	})
	if err != nil {
		t.Fatal("failed to create watcher", err)
	}
	err = w.load(root, true)
	if err != nil {
		t.Fatal("failed to add root watch", err)
	}
	env.watcher = w
	return env
}

func (t *testenv) handle(e Event, i FileInfo) {
	t.Log("record", e, i.Path())
	t.Lock()
	defer t.Unlock()
	t.events = append(t.events, record{e, i.Path(), false})
}

func (t *testenv) error(err error) {
	t.Log("record", err)
	t.Lock()
	defer t.Unlock()
	t.errors = append(t.errors, err)
}

func (t *testenv) close() error {
	err := t.watcher.close()
	if err != nil {
		os.RemoveAll(t.root)
		return err
	}
	return os.RemoveAll(t.root)
}

func (t *testenv) writeClose(f *os.File, err error) {
	if err != nil {
		t.Fatal("failed to create.", err)
	}
	_, err = fmt.Fprintln(f, "hello world")
	if err != nil {
		f.Close()
		t.Fatal("failed to write.", err)
	}
	err = f.Sync()
	if err != nil {
		f.Close()
		t.Fatal("failed to sync.", err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal("failed to close.", err)
	}
}

func (t *testenv) createWriteClose(paths ...string) string {
	path := filepath.Join(paths...)
	t.writeClose(os.Create(path))
	t.expect = append(t.expect, record{Create, path, false}, record{Modify, path, true})
	return path
}

func (t *testenv) openWriteClose(paths ...string) string {
	path := filepath.Join(paths...)
	t.writeClose(os.Create(path))
	t.expect = append(t.expect, record{Modify, path, true})
	return path
}

func (t *testenv) mkdir(paths ...string) string {
	path := filepath.Join(paths...)
	err := os.Mkdir(path, 0700)
	if err != nil {
		t.Fatal("failed to mkdir.", err)
	}
	t.expect = append(t.expect, record{Create, path, false})
	return path
}

func (t *testenv) remove(path string) {
	err := os.RemoveAll(path)
	if err != nil {
		t.Fatal("failed to remove.", err)
	}
	t.expect = append(t.expect, record{Delete, path, false})
}

func (t *testenv) load(path string, recursive bool) {
	err := t.watcher.load(path, recursive)
	if err != nil {
		t.Fatal("failed to load.", err)
	}
}

func (t *testenv) unload(path string, recursive bool) {
	err := t.watcher.unload(path, recursive)
	if err != nil {
		t.Fatal("failed to unload.", err)
	}
}

func (t *testenv) check() {
	t.Lock()
	defer t.Unlock()
	for _, err := range t.errors {
		if err != nil {
			t.Error(err)
		}
	}
	opt := 0
	for i, e := range t.expect {
		if i-opt >= len(t.events) {
			t.Errorf("expected %s got nothing", e)
			continue
		}
		record := t.events[i-opt]
		if record.Event == e.Event && record.path == e.path {
			continue
		}
		if e.optional {
			opt++
			continue
		}
		t.Errorf("expected %s got %s", e, record)
	}
	if len(t.events) > len(t.expect) {
		for _, record := range t.events[len(t.expect):] {
			t.Errorf("unexpected %s", record)
		}
	}
}
