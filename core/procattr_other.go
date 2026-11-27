//go:build !linux && !windows

package core

import "syscall"

// workerSysProcAttr: the worker watches its parent pipe for the supervisor's exit.
func workerSysProcAttr() *syscall.SysProcAttr { return nil }
