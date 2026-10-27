//go:build windows

package forward

import "syscall"

// WSAEADDRINUSE
const errAddrInUse = syscall.Errno(10048)
