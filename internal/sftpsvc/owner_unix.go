//go:build !windows

package sftpsvc

import (
	"io/fs"
	"syscall"
)

// localIDs are the owner and the group of a local file.
func localIDs(fi fs.FileInfo) (uid, gid uint32, ok bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Uid, st.Gid, true
	}
	return 0, 0, false
}
