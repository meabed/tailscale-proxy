//go:build !windows

package core

import (
	"fmt"
	"os"
	"syscall"
)

// stopProcess asks pid to exit (SIGTERM).
func stopProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

// dropPrivileges switches a root process to uid/gid for good (after it has bound
// its privileged ports), so every file it then creates belongs to that user.
func dropPrivileges(uid, gid int) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("--uid needs the proxy to start as root (it binds port 443, then runs as uid %d)", uid)
	}
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups: %v", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("setgid %d: %v", gid, err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("setuid %d: %v", uid, err)
	}
	return nil
}
