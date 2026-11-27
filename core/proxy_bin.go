package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The local HTTPS proxy runs the user's own copies of tsp, kept in ~/.tsp/bin: one
// file per release (tsp-1.14.0) and bin/state.json naming the current one, the one
// before it (the rollback), and the releases that failed to start (never tried
// again). Any newer tsp the user runs copies itself in (syncProxyBin), the daily
// auto-update downloads one (updateProxyBin), and the supervisor swaps to it live
// once it proves it serves (supervisor_unix.go). Nothing here needs sudo.

// binState is bin/state.json.
type binState struct {
	Current  string            `json:"current,omitempty"`
	Previous string            `json:"previous,omitempty"`
	Bad      map[string]string `json:"bad,omitempty"` // release → why it was rolled back
}

func binDir(dir string) string           { return filepath.Join(dir, "bin") }
func binStatePath(dir string) string     { return filepath.Join(binDir(dir), "state.json") }
func binLockPath(dir string) string      { return filepath.Join(binDir(dir), "state.lock") }
func proxyBinName(version string) string { return "tsp-" + normalizeVer(version) + exeSuffix() }

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// proxyBinPath is release version's copy in bin/.
func proxyBinPath(dir, version string) string {
	return filepath.Join(binDir(dir), proxyBinName(version))
}

func loadBinState(dir string) binState {
	var st binState
	if data, err := os.ReadFile(binStatePath(dir)); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func saveBinState(dir string, st binState) error {
	data, _ := json.MarshalIndent(st, "", "  ")
	return writeFileAtomic(binStatePath(dir), append(data, '\n'), 0o644)
}

// updateBinState changes bin/state.json under its lock (the CLI, the auto-update
// and the supervisor all write it).
func updateBinState(dir string, change func(*binState) error) error {
	release, _, err := waitLock(binLockPath(dir), 10*time.Second)
	if err != nil {
		return err
	}
	if release != nil {
		defer release()
	}
	st := loadBinState(dir)
	if err := change(&st); err != nil {
		return err
	}
	return saveBinState(dir, st)
}

// proxyBinVersion is the current copy's release, "" when there is none.
func proxyBinVersion(dir string) string {
	v := loadBinState(dir).Current
	if v == "" {
		return ""
	}
	if _, err := os.Stat(proxyBinPath(dir, v)); err != nil {
		return ""
	}
	return v
}

// isBadVersion reports whether release v failed to start here before.
func isBadVersion(dir, v string) bool {
	_, bad := loadBinState(dir).Bad[normalizeVer(v)]
	return bad
}

// installProxyBin copies the binary at src in as release version and makes it
// current, keeping the one it replaces as the rollback. Older copies are removed.
func installProxyBin(dir, src, version string) error {
	version = normalizeVer(version)
	if err := copyExecutable(src, proxyBinPath(dir, version)); err != nil {
		return err
	}
	return updateBinState(dir, func(st *binState) error {
		if st.Current != version {
			st.Previous, st.Current = st.Current, version
		}
		pruneProxyBins(dir, st.Current, st.Previous)
		return nil
	})
}

// pruneProxyBins removes the copies in bin/ other than keep.
func pruneProxyBins(dir string, keep ...string) {
	entries, err := os.ReadDir(binDir(dir))
	if err != nil {
		return
	}
	wanted := map[string]bool{}
	for _, v := range keep {
		if v != "" {
			wanted[proxyBinName(v)] = true
		}
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "tsp-") && !wanted[n] {
			_ = os.Remove(filepath.Join(binDir(dir), n))
		}
	}
}

// markBad records that release v failed to start and points current back at the
// previous copy when v was current. It returns the release that is current now.
func markBad(dir, v, why string) string {
	v = normalizeVer(v)
	var now string
	_ = updateBinState(dir, func(st *binState) error {
		if st.Bad == nil {
			st.Bad = map[string]string{}
		}
		st.Bad[v] = why
		if st.Current == v && st.Previous != "" {
			st.Current, st.Previous = st.Previous, ""
		}
		now = st.Current
		return nil
	})
	return now
}

// syncProxyBin copies self in as the current copy when self is newer (never
// older, never a release that failed here), once local HTTPS is set up on this
// machine (bin/ exists). It reports whether it copied. Development builds never copy.
func syncProxyBin(dir, self string) (bool, error) {
	if Version == "dev" {
		return false, nil
	}
	if _, err := os.Stat(binDir(dir)); err != nil {
		return false, nil
	}
	if cur := proxyBinVersion(dir); cur != "" && !versionLess(cur, Version) {
		return false, nil
	}
	if isBadVersion(dir, Version) {
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
