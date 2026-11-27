//go:build !windows

package core

import (
	"os"
	"syscall"
)

// ownedByMe reports whether fi belongs to this process's user.
func ownedByMe(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// othersCanWrite reports whether a user other than the owner may change fi: group-
// or world-writable (a sticky shared folder like /tmp included).
func othersCanWrite(fi os.FileInfo) bool { return fi.Mode().Perm()&0o022 != 0 }

// worldWritable reports whether anyone may create files in folder fi (/tmp).
func worldWritable(fi os.FileInfo) bool { return fi.Mode().Perm()&0o002 != 0 }
