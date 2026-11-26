//go:build windows

package core

import (
	"fmt"
	"os"
)

// stopProcess ends pid (Windows has no SIGTERM).
func stopProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// dropPrivileges is not available on Windows, where no port needs elevation.
func dropPrivileges(uid, gid int) error {
	return fmt.Errorf("--uid is not supported on Windows")
}
