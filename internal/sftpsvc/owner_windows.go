//go:build windows

package sftpsvc

import "io/fs"

// localIDs: Windows files have no numeric owner.
func localIDs(fs.FileInfo) (uid, gid uint32, ok bool) { return 0, 0, false }
