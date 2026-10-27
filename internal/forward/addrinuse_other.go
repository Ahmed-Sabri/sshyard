//go:build !windows

package forward

import "syscall"

const errAddrInUse = syscall.EADDRINUSE
