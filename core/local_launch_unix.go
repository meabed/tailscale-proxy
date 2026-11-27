//go:build !windows

package core

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The boot service runs as root only long enough to bind 443: the launcher binds,
// drops to the user for good, then execs the user's own tsp (~/.tailscale-proxy/
// bin/tsp) with the listening sockets inherited. Root never runs a file the user
// can write: the exec happens after the drop. The user's tsp serves, and exits
// when a newer one lands in bin/ (watchProxyBin); launchd/systemd restart the
// launcher, which hands the sockets to the new one. Updates never need sudo.

// The environment a launcher hands to the proxy it execs.
const (
	envListenFDs  = "TSP_LISTEN_FDS"  // comma-separated inherited listener fds
	envListenPort = "TSP_LISTEN_PORT" // the port they listen on
	envLauncher   = "TSP_LAUNCHER"    // the launcher's tsp release
)

// launchProbeTimeout bounds `tsp --version` on the user's copy before the exec.
var launchProbeTimeout = 5 * time.Second

// handOff execs the user's proxy copy in dir with lns inherited. It returns only
// when it did not exec (no copy, an older or broken one): the caller then serves.
func handOff(lns []net.Listener, dir string, port int) error {
	bin := proxyBinPath(dir)
	v := proxyBinVersion(dir)
	switch {
	case v == "":
		return fmt.Errorf("no tsp at %s yet", bin)
	case Version != "dev" && versionLess(v, Version):
		return fmt.Errorf("%s is tsp %s, older than this launcher (%s)", bin, v, Version)
	}
	if err := probeBinary(bin, v); err != nil {
		return err
	}
	files := make([]*os.File, 0, len(lns))
	fds := make([]string, 0, len(lns))
	for _, ln := range lns {
		tl, ok := ln.(*net.TCPListener)
		if !ok {
			return fmt.Errorf("listener %s is not TCP", ln.Addr())
		}
		f, err := tl.File() // a dup, close-on-exec: cleared below so the exec keeps it
		if err != nil {
			return err
		}
		if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_SETFD, 0); errno != 0 {
			return fmt.Errorf("fcntl: %v", errno)
		}
		files = append(files, f)
		fds = append(fds, strconv.Itoa(int(f.Fd())))
	}
	env := append(withoutEnv(os.Environ(), envListenFDs, envListenPort, envLauncher),
		envListenFDs+"="+strings.Join(fds, ","), envListenPort+"="+strconv.Itoa(port), envLauncher+"="+Version)
	log.Printf("tsp launcher %s: port %d handed to %s (tsp %s)", Version, port, bin, v)
	err := syscall.Exec(bin, []string{bin, "proxy"}, env)
	runtime.KeepAlive(files) // the dups must stay open until the exec
	return fmt.Errorf("exec %s: %v", bin, err)
}

// probeBinary runs bin --version and wants version back: a copy that does not run
// (wrong platform, truncated) is never exec'd in place of a working launcher.
func probeBinary(bin, version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), launchProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return fmt.Errorf("%s --version: %v", bin, err)
	}
	if got := strings.TrimSpace(string(out)); normalizeVer(got) != normalizeVer(version) {
		return fmt.Errorf("%s --version printed %q, bin/version says %s", bin, got, version)
	}
	return nil
}

// inheritedListeners returns the sockets a launcher handed over, if any, and
// clears the variables so nothing this proxy starts inherits them.
func inheritedListeners() (lns []net.Listener, port int, launcher string, ok bool, err error) {
	spec := os.Getenv(envListenFDs)
	if spec == "" {
		return nil, 0, "", false, nil
	}
	port, _ = strconv.Atoi(os.Getenv(envListenPort))
	launcher = os.Getenv(envLauncher)
	for _, k := range []string{envListenFDs, envListenPort, envLauncher} {
		_ = os.Unsetenv(k)
	}
	for _, s := range strings.Split(spec, ",") {
		fd, convErr := strconv.Atoi(s)
		if convErr != nil || fd < 3 {
			return nil, 0, "", true, fmt.Errorf("bad %s %q", envListenFDs, spec)
		}
		f := os.NewFile(uintptr(fd), "tsp-listener-"+s)
		ln, lnErr := net.FileListener(f) // dups the fd
		_ = f.Close()
		if lnErr != nil {
			return nil, 0, "", true, fmt.Errorf("inherited fd %d: %v", fd, lnErr)
		}
		lns = append(lns, ln)
	}
	return lns, port, launcher, true, nil
}

func withoutEnv(env []string, keys ...string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		keep := true
		for _, drop := range keys {
			if k == drop {
				keep = false
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}
