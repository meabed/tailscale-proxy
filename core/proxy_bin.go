package core

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The boot service runs the user's own copy of tsp, ~/.tailscale-proxy/bin/tsp, as
// that user (the root launcher binds 443, drops to the user, then execs it: see
// local_launch_unix.go). Any newer tsp the user runs copies itself there, and the
// proxy restarts on it, so updating tsp never needs sudo. bin/version names the
// copy's release, read without running it.

func proxyBinPath(dir string) string {
	name := "tsp"
	if runtime.GOOS == "windows" {
		name = "tsp.exe"
	}
	return filepath.Join(dir, "bin", name)
}

func proxyBinVersionPath(dir string) string { return filepath.Join(dir, "bin", "version") }

// proxyBinVersion is the release of the user's proxy copy, "" when there is none.
func proxyBinVersion(dir string) string {
	data, err := os.ReadFile(proxyBinVersionPath(dir))
	if err != nil {
		return ""
	}
	if _, err := os.Stat(proxyBinPath(dir)); err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// installProxyBin puts the binary at src in place as the proxy copy of release
// version: the binary first, then its version (what a running proxy watches).
func installProxyBin(dir, src, version string) error {
	if err := copyExecutable(src, proxyBinPath(dir)); err != nil {
		return err
	}
	return writeFileAtomic(proxyBinVersionPath(dir), []byte(version+"\n"), 0o644)
}

// syncProxyBin copies self in as the proxy copy when self is newer (never older),
// once local HTTPS is set up on this machine (bin/ exists). It reports whether it
// copied. Development builds never copy.
func syncProxyBin(dir, self string) (bool, error) {
	if Version == "dev" {
		return false, nil
	}
	if _, err := os.Stat(filepath.Dir(proxyBinPath(dir))); err != nil {
		return false, nil
	}
	if cur := proxyBinVersion(dir); cur != "" && !versionLess(cur, Version) {
		return false, nil
	}
	return true, installProxyBin(dir, self, Version)
}

// versionLess reports whether release a is older than b ("1.9.0" < "1.12.0", a
// leading v ignored). A version that does not parse is older than any that does.
func versionLess(a, b string) bool {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return !okA && okB
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	core, _, _ := strings.Cut(normalizeVer(v), "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// proxyBinPoll is how often a running proxy looks for a newer copy in bin/.
var proxyBinPoll = 3 * time.Second

// watchProxyBin calls restart once bin/ holds a release newer than this proxy that
// arrived after it started (a copy that was already there and failed to start
// never loops the service).
func watchProxyBin(ctx context.Context, dir string, restart func()) {
	start := proxyBinVersion(dir)
	t := time.NewTicker(proxyBinPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if v := proxyBinVersion(dir); v != start && v != "" && versionLess(Version, v) {
				log.Printf("tsp %s is in %s: restarting on it", v, proxyBinPath(dir))
				restart()
				return
			}
		}
	}
}
