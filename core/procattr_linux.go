package core

import "syscall"

// workerSysProcAttr: a worker gets SIGTERM if its supervisor dies (its parent-pipe
// EOF covers the same on other systems).
func workerSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
