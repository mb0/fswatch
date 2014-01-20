// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fswatch

import "os"

// tree represents a map of string paths to info pointers.
// it is implemented as a critbit tree from the package:
// 	github.com/mb0/critbit
type tree struct {
	root   *ref
}

// ref holds either a info or node pointer
type ref struct {
	info *info
	node *node
}

// node represents a tree branch that holds two refs and their critical bit
type node struct {
	child [2]ref
	off   int
	bit   byte
}

// replaceSep recplaces path separators with the byte value 1 to make
// the traversal order compatible with `filepath.Walk`
func replaceSep(ch byte) byte {
	if ch == os.PathSeparator {
		return 0x01
	}
	return ch
}

// dir calculates the direction for the given key
func (n *node) dir(key string) byte {
	if n.off < len(key) {
		ch := key[n.off]
		// manual inline of replaceSep to keep
		// this method itself inlineable
		if ch == os.PathSeparator {
			ch = 0x01
		}
		if ch&n.bit != 0 {
			return 1
		}
	}
	return 0
}

// get returns an existing info pointer for path or nil
func (t *tree) get(path string) *info {
	// test for empty tree
	if t.root == nil {
		return nil
	}
	// walk for best member
	p := *t.root
	for p.node != nil {
		// try next node
		p = p.node.child[p.node.dir(path)]
	}
	// check for membership
	if path != p.info.path {
		return nil
	}
	return p.info
}

// get inserts an info pointer into the tree or returns an existing one with the same path
func (t *tree) insert(info *info) *info {
	// test for empty tree
	if t.root == nil {
		t.root = &ref{info: info}
		return nil
	}
	// walk for best member
	p := *t.root
	for p.node != nil {
		// try next node
		p = p.node.child[p.node.dir(info.path)]
	}
	// find critical bit
	var off int
	var ch, bit byte
	// find differing byte
	for off = 0; off < len(info.path); off++ {
		if ch = 0; off < len(p.info.path) {
			ch = replaceSep(p.info.path[off])
		}
		if keych := replaceSep(info.path[off]); ch != keych {
			bit = ch ^ keych
			goto ByteFound
		}
	}
	if off < len(p.info.path) {
		ch = replaceSep(p.info.path[off])
		bit = ch
		goto ByteFound
	}
	return p.info
ByteFound:
	// find differing bit
	bit |= bit >> 1
	bit |= bit >> 2
	bit |= bit >> 4
	bit = bit &^ (bit >> 1)
	var ndir byte
	if ch&bit != 0 {
		ndir++
	}
	// insert new node
	nn := &node{off: off, bit: bit}
	nn.child[1-ndir].info = info
	// walk for best insertion node
	wp := t.root
	for wp.node != nil {
		p = *wp
		if p.node.off > off || p.node.off == off && p.node.bit < bit {
			break
		}
		// try next node
		wp = &p.node.child[p.node.dir(info.path)]
	}
	nn.child[ndir] = *wp
	wp.node = nn
	return nil
}

// delete deletes the info at root and all its descendents from the tree
// and calls the given handler funcion in traversal order
func (t *tree) deleteAll(root string, f func(*info)) {
	// test for empty tree
	if t.root == nil {
		return
	}
	// walk for best member
	var dir byte
	var wp *ref
	p := t.root
	for p.node != nil {
		wp = p
		// try next node
		dir = p.node.dir(root)
		p = &p.node.child[dir]
	}
	// check for membership
	info := p.info
	if root != info.path {
		return
	}
	// delete from tree
	if wp == nil {
		t.root = nil
	} else {
		*wp = wp.node.child[1-dir]
	}
	f(info)
	// return if not directory or empty tree
	if !info.IsDir() || t.root == nil {
		return
	}
	// delete subtree
	root += string(os.PathSeparator)
	// walk for best member
	p, top, wp := wp, wp, nil
	for p.node != nil {
		newtop := p.node.off < len(root)
		if newtop {
			wp = p
		}
		ndir := p.node.dir(root)
		p = &p.node.child[dir]
		if newtop {
			dir = ndir
			top = p
		}
	}
	if len(p.info.path) < len(root) {
		return
	}
	for i := 0; i < len(root); i++ {
		if p.info.path[i] != root[i] {
			return
		}
	}
	if wp == nil {
		t.root = nil
	} else {
		*wp = wp.node.child[1-dir]
	}
	t.deliter(*top, f)
}

func (t *tree) deliter(p ref, f func(*info)) {
	if p.node != nil {
		t.deliter(p.node.child[0], f)
		t.deliter(p.node.child[1], f)
	} else {
		f(p.info)
	}
}

// walk traverses the info at root and all its descendents from the tree
// and calls the given handler funcion in traversal order.
// walk will not descend into a directory when the handler returns `SkipDir`.
func (t *tree) walk(root string, f func(FileInfo) error) error {
	fi := t.get(root)
	if fi == nil || fi.Ignored() {
		return &os.PathError{Op: "stat", Path: root, Err: os.ErrNotExist}
	}
	err := f(fi)
	if !fi.IsDir() || err != nil {
		return err
	}
	// walk for best member
	root += string(os.PathSeparator)
	p, top := *t.root, *t.root
	for p.node != nil {
		newtop := p.node.off < len(root)
		// try next node
		p = p.node.child[p.node.dir(root)]
		if newtop {
			top = p
		}
	}
	if len(p.info.path) < len(root) {
		return nil
	}
	for i := 0; i < len(root); i++ {
		if p.info.path[i] != root[i] {
			return nil
		}
	}
	return walkiter(top, f, nil)
}

type skip string

func (s skip) Error() string { return string(s) }

func walkiter(p ref, f func(FileInfo) error, skips []string) error {
	if p.node != nil {
		for i := 0; i < 2; i++ {
			err := walkiter(p.node.child[i], f, skips)
			switch err.(type) {
			case nil:
				continue
			case skip:
				skips = append(skips, err.Error())
			default:
				return err
			}
		}
		return nil
	}
	// skip this info if it is prefixed or itself is a skip path
	path := p.info.path
Skips:
	for _, s := range skips {
		if len(s) > len(path) {
			continue
		}
		// compare starting with the end
		for i := len(s) - 1; i >= 0; i-- {
			if s[i] != path[i] {
				continue Skips
			}
		}
		return nil
	}
	if p.info.Ignored() {
		return nil
	}
	err := f(p.info)
	if err == SkipDir && p.info.IsDir() {
		// if we skip this dir we need to know the path later
		return skip(path + string(os.PathSeparator))
	}
	return err
}
