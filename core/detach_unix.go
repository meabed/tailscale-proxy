//go:build !windows

package core

import "syscall"

// detachSysProcAttr starts the child in its own session.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
