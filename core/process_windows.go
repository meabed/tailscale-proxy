//go:build windows

package core

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
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

// requestFromOwner refuses every stop from the dashboard: Windows gives no cheap
// way to see whose program holds the other end of a loopback connection, and a
// stop tsp cannot attribute to the user is not made. The dashboard shows the
// commands instead, and tsp stop runs as the user in a terminal.
var requestFromOwner = func(*http.Request) error {
	return fmt.Errorf("on Windows the dashboard cannot tell whose program asks, so it does not stop apps: run tsp stop <name> in a terminal")
}

// psTable is empty on Windows: tasklist has no CPU time or parent, so the
// dashboard shows no CPU or memory there, and taskkill /T finds the tree itself.
func psTable() map[int]psRow { return nil }

// signalStop ends pid and the processes it started: taskkill /T asks them to
// close, /F (forced) ends them. A process that is gone already counts as stopped.
func signalStop(pid int, force bool) error {
	if !pidRunning(pid) {
		return nil
	}
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	if out, err := exec.Command("taskkill", args...).CombinedOutput(); err != nil && pidRunning(pid) {
		msg := strings.TrimSpace(string(out))
		if !force && strings.Contains(strings.ToLower(msg), "forcefully") {
			return nil // a console app: the wait reports it is still running, and --force ends it
		}
		return fmt.Errorf("taskkill %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}

// pidRunning reports whether pid is a live process.
func pidRunning(pid int) bool {
	const queryLimited, stillActive = 0x1000, 259
	h, err := syscall.OpenProcess(queryLimited, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	return syscall.GetExitCodeProcess(h, &code) == nil && code == stillActive
}

// processCommands are the process names of pids (tasklist).
func processCommands(pids []int) map[int]string {
	out := map[int]string{}
	if len(pids) == 0 {
		return out
	}
	data, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return out
	}
	for pid, name := range parseTasklist(string(data)) {
		if slices.Contains(pids, pid) {
			out[pid] = shortCommand(name)
		}
	}
	return out
}
