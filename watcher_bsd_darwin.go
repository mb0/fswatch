// Copyright 2013 Martin Schnabel.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build darwin

package fswatch

import "syscall"

func init() {
	openwdFlags = syscall.O_EVTONLY
}
