//go:build windows

package core

import (
	"errors"
	"os"
	"syscall"
)

// tryLock opens path with no sharing allowed: while one process has it open, no
// other can, and Windows closes it when that process ends for any reason.
func tryLock(path string) (release func(), ok bool, err error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, errSharingViolation) {
			return nil, false, nil
		}
		return nil, false, &os.PathError{Op: "lock", Path: path, Err: err}
	}
	return func() { _ = syscall.CloseHandle(h) }, true, nil
}

// removeLocked deletes path, whose lock the caller holds, then gives the lock up.
// Windows opens a lock file for one handle at a time, so it is closed first and
// removed after; a process that opens it in between simply holds a new file.
func removeLocked(path string, release func()) {
	release()
	_ = os.Remove(path)
}

// checkRootOnly: the boot service is macOS/Linux only.
func checkRootOnly(string) error { return nil }

// errSharingViolation is ERROR_SHARING_VIOLATION (32): the file is open elsewhere.
const errSharingViolation = syscall.Errno(32)
