// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fswatch

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWalk(t *testing.T) {
	paths := make([]string, 0, 1000)
	walker := filepath.WalkFunc(func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.Name() == "runtime" {
			return filepath.SkipDir
		}
		paths = append(paths, path)
		return nil
	})
	gosrc := filepath.Join(runtime.GOROOT(), "src")
	golib := filepath.Join(gosrc, "pkg")
	err := filepath.Walk(golib, walker)
	if err != nil && err != filepath.SkipDir {
		t.Fatal(err)
	}
	expect := append(make([]string, 0, len(paths)), paths...)
	paths = paths[:0]
	tr, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	err = tr.Load(gosrc, true)
	if err != nil {
		t.Fatal(err)
	}
	err = tr.Walk(golib, walker)
	if err != nil && err != filepath.SkipDir {
		t.Fatal(err)
	}
	minl := len(expect)
	if len(expect) != len(paths) {
		t.Errorf("expected %d paths got %d", len(expect), len(paths))
		if len(expect) > len(paths) {
			minl = len(paths)
		}
	}
	fails := 0
	for i := 0; i < minl; i++ {
		if expect[i] != paths[i] {
			t.Errorf("no match at %d expect %s got %s", i, expect[i], paths[i])
			if fails++; fails < 10 {
				continue
			}
			t.Errorf("too many errors")
			break
		}
	}
}
