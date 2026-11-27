//go:build !windows

package core

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
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
// this user's processes (a browser, curl), not another account's: lsof shows the
// client's end of this very connection (its local end is the request's remote
// address, its remote end the proxy's own), and only for processes the user may
// inspect, so another account's client is never found.
var requestFromOwner = func(r *http.Request) error {
	client, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !client.Addr().IsLoopback() {
		return fmt.Errorf("only this machine may stop apps (request from %s)", r.RemoteAddr)
	}
	server, _ := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if server == nil {
		return fmt.Errorf("the proxy's end of the connection is unknown")
	}
	want := client.String() + "->" + server.String()
	out, _ := exec.Command("lsof", "-nP", "-iTCP@"+client.String(), "-sTCP:ESTABLISHED", "-Fpun").Output()
	pid, owner := 0, ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
			owner = ""
		case strings.HasPrefix(line, "u"):
			owner = line[1:]
		case strings.HasPrefix(line, "n") && line[1:] == want && pid != os.Getpid() && owner == strconv.Itoa(os.Getuid()):
			return nil
		}
	}
	return fmt.Errorf("only your own programs may stop your apps: the connection %s does not belong to a process of uid %d", want, os.Getuid())
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
