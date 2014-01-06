// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build freebsd openbsd netbsd darwin linux

package fswatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLinks(t *testing.T) {
	env := newtestenv(t)
	defer env.close()

	link := filepath.Join(env.root, "link")
	target := filepath.Join(env.root, "none")
	err := os.Symlink(target, link)
	if err != nil {
		t.Fatal("failed to create symlink", err)
	}
	time.Sleep(time.Millisecond)
	env.expect = []record{{Create, link, false}}
	env.check()
}
