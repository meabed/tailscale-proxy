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
