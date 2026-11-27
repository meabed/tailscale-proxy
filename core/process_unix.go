//go:build !windows

package core

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// requestFromOwner checks that a request to the local proxy comes from one of
// this user's processes (a browser, curl), not another account's: on loopback,
// lsof shows the client's end of the connection, and only for processes the user
// may inspect.
var requestFromOwner = func(remoteAddr string) error {
	host, port, err := net.SplitHostPort(remoteAddr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("only this machine may stop apps (request from %s)", remoteAddr)
	}
	if ip.To4() == nil {
		host = "[" + host + "]"
	}
	out, _ := exec.Command("lsof", "-nP", "-iTCP@"+host+":"+port, "-sTCP:ESTABLISHED", "-Fpu").Output()
	pid, uid := 0, strconv.Itoa(os.Getuid())
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "u") && pid != os.Getpid() && line[1:] == uid:
			return nil
		}
	}
	return fmt.Errorf("only your own programs may stop your apps: the request did not come from a process of uid %s", uid)
}

// signalStop sends pid SIGTERM, or SIGKILL when forced. A process that is gone
// already counts as stopped.
func signalStop(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	switch err := syscall.Kill(pid, sig); {
	case err == nil, errors.Is(err, syscall.ESRCH):
		return nil
	case errors.Is(err, syscall.EPERM):
		return fmt.Errorf("pid %d belongs to another user: sudo kill %d", pid, pid)
	default:
		return fmt.Errorf("kill %d: %v", pid, err)
	}
}

// pidRunning reports whether pid is a live process (a zombie counts as gone).
func pidRunning(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}

// processCommands are the command lines of pids (ps), shortened.
func processCommands(pids []int) map[int]string {
	out := map[int]string{}
	if len(pids) == 0 {
		return out
	}
	data, err := exec.Command("ps", "-o", "pid=,command=", "-p", joinPIDs(pids, ",")).Output()
	if err != nil && len(data) == 0 {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		pid, cmd, ok := strings.Cut(strings.TrimSpace(line), " ")
		if n, err := strconv.Atoi(pid); ok && err == nil {
			out[n] = shortCommand(cmd)
		}
	}
	return out
}
