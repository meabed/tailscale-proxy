package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withVersion runs a test as tsp release v (tests build as "dev", which never syncs).
func withVersion(t *testing.T, v string) {
	t.Helper()
	orig := Version
	Version = v
	t.Cleanup(func() { Version = orig })
}

func stateFor(t *testing.T) string {
	home, _ := isolate(t)
	dir := filepath.Join(home, ".tailscale-proxy")
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"1.9.0", "1.12.0", true},
		{"v1.12.0", "1.12.0", false},
		{"1.12.1", "1.12.0", false},
		{"2.0.0", "10.0.0", true},
		{"dev", "1.0.0", true},
		{"1.0.0", "dev", false},
		{"1.13.0-rc.1", "1.13.0", false},
	} {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}

func TestSyncProxyBin(t *testing.T) {
	home, _ := isolate(t)
	dir := filepath.Join(home, ".tailscale-proxy")
	self := filepath.Join(t.TempDir(), "tsp")
	_ = os.WriteFile(self, []byte("new"), 0o755)
	withVersion(t, "1.13.0")

	if copied, _ := syncProxyBin(dir, self); copied {
		t.Fatal("copied before local HTTPS was set up (no bin/)")
	}
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	if copied, err := syncProxyBin(dir, self); !copied || err != nil {
		t.Fatalf("first sync: copied=%v err=%v", copied, err)
	}
	if got := proxyBinVersion(dir); got != "1.13.0" {
		t.Errorf("bin/version = %q", got)
	}
	if copied, _ := syncProxyBin(dir, self); copied {
		t.Error("copied the same release again")
	}
	_ = installProxyBin(dir, self, "1.14.0") // the proxy updated itself past this CLI
	if copied, _ := syncProxyBin(dir, self); copied || proxyBinVersion(dir) != "1.14.0" {
		t.Error("an older CLI replaced a newer proxy copy")
	}
	withVersion(t, "dev")
	_ = os.Remove(proxyBinVersionPath(dir))
	if copied, _ := syncProxyBin(dir, self); copied {
		t.Error("a development build copied itself")
	}
}

func TestWatchProxyBin(t *testing.T) {
	dir := stateFor(t)
	withVersion(t, "1.13.0")
	orig := proxyBinPoll
	proxyBinPoll = 5 * time.Millisecond
	t.Cleanup(func() { proxyBinPoll = orig })
	src := filepath.Join(t.TempDir(), "tsp")
	_ = os.WriteFile(src, []byte("x"), 0o755)
	_ = installProxyBin(dir, src, "1.14.0") // there before the proxy started (it failed to run): no loop

	var restarts atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { watchProxyBin(ctx, dir, func() { restarts.Add(1) }); close(done) }()
	time.Sleep(30 * time.Millisecond)
	if restarts.Load() != 0 {
		t.Fatal("restarted on a copy that was already there")
	}
	_ = installProxyBin(dir, src, "1.12.0") // older than this proxy: stays
	time.Sleep(30 * time.Millisecond)
	if restarts.Load() != 0 {
		t.Fatal("restarted on an older copy")
	}
	_ = installProxyBin(dir, src, "1.15.0")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("no restart on a newer copy")
	}
	if restarts.Load() != 1 {
		t.Errorf("restarts = %d", restarts.Load())
	}
}

// releaseServer serves a GitHub-like latest release and its assets.
func releaseServer(t *testing.T, tag string, binary []byte, corrupt bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "tsp", Mode: 0o755, Size: int64(len(binary))})
	_, _ = tw.Write(binary)
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(archive.Bytes())
	name := releaseArchiveName(runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		t.Skip("the release fixture is a .tar.gz")
	}
	if corrupt {
		sum[0] ^= 0xff
	}
	var downloads atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"e1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"e1"`)
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tag})
	})
	mux.HandleFunc("/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n%s  other.zip\n", hex.EncodeToString(sum[:]), name, strings.Repeat("0", 64))
	})
	mux.HandleFunc("/download/"+tag+"/"+name, func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		_, _ = w.Write(archive.Bytes())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	origAPI, origDL := releaseAPI, releaseDownload
	releaseAPI, releaseDownload = srv.URL+"/latest", srv.URL+"/download"
	t.Cleanup(func() { releaseAPI, releaseDownload = origAPI, origDL })
	return srv, &downloads
}

func TestUpdateProxyBin_installsAVerifiedNewerRelease(t *testing.T) {
	dir := stateFor(t)
	withVersion(t, "1.12.0")
	_, downloads := releaseServer(t, "v1.13.0", []byte("#!/bin/sh\necho 1.13.0\n"), false)

	tag, err := updateProxyBin(context.Background(), dir)
	if err != nil || tag != "1.13.0" {
		t.Fatalf("tag=%q err=%v", tag, err)
	}
	if got, _ := os.ReadFile(proxyBinPath(dir)); !strings.Contains(string(got), "echo 1.13.0") || proxyBinVersion(dir) != "1.13.0" {
		t.Errorf("bin/tsp = %q, version %q", got, proxyBinVersion(dir))
	}
	st := loadUpdateState(dir)
	if st.ETag != `"e1"` || st.Latest != "v1.13.0" || st.CheckedAt.IsZero() {
		t.Errorf("update.json = %+v", st)
	}
	// The next check is a 304 and installs nothing more.
	if tag, err := updateProxyBin(context.Background(), dir); tag != "" || err != nil || downloads.Load() != 1 {
		t.Errorf("second check: tag=%q err=%v downloads=%d", tag, err, downloads.Load())
	}
}

func TestUpdateProxyBin_rejectsABadChecksum(t *testing.T) {
	dir := stateFor(t)
	withVersion(t, "1.12.0")
	releaseServer(t, "v1.13.0", []byte("tampered"), true)
	if _, err := updateProxyBin(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want a checksum error", err)
	}
	if proxyBinVersion(dir) != "" {
		t.Error("a binary that failed its checksum was installed")
	}
}

func TestUpdateProxyBin_currentDownloadsNothing(t *testing.T) {
	dir := stateFor(t)
	withVersion(t, "1.13.0")
	_, downloads := releaseServer(t, "v1.13.0", []byte("x"), false)
	if tag, err := updateProxyBin(context.Background(), dir); tag != "" || err != nil || downloads.Load() != 0 {
		t.Errorf("tag=%q err=%v downloads=%d", tag, err, downloads.Load())
	}
}

func TestUpdateNotice_oncePerDay(t *testing.T) {
	dir := stateFor(t)
	withVersion(t, "1.12.0")
	saveUpdateState(dir, updateState{Latest: "v1.13.0", CheckedAt: time.Now()})
	now := time.Now()
	line := updateNotice(dir, "/opt/homebrew/Caskroom/tsp/1.12.0/tsp", now)
	if line != "tsp 1.13.0 is out (this is 1.12.0): brew upgrade tsp" {
		t.Errorf("notice = %q", line)
	}
	if again := updateNotice(dir, "/opt/homebrew/Caskroom/tsp/1.12.0/tsp", now.Add(time.Hour)); again != "" {
		t.Errorf("said twice in a day: %q", again)
	}
	if next := updateNotice(dir, "/usr/lib/node_modules/tailscale-proxy/bin/tsp", now.Add(25*time.Hour)); !strings.HasSuffix(next, "npm install -g tailscale-proxy@latest") {
		t.Errorf("next day = %q", next)
	}
	withVersion(t, "1.13.0")
	if line := updateNotice(dir, "/usr/local/bin/tsp", now.Add(72*time.Hour)); line != "" {
		t.Errorf("notice when current: %q", line)
	}
}

func TestAutoUpdateEnabled(t *testing.T) {
	isolate(t)
	t.Setenv("CI", "")
	t.Setenv("TSP_NO_UPDATE", "")
	if !autoUpdateEnabled() {
		t.Error("off by default")
	}
	t.Setenv("TSP_NO_UPDATE", "1")
	if autoUpdateEnabled() {
		t.Error("TSP_NO_UPDATE=1 left it on")
	}
	t.Setenv("TSP_NO_UPDATE", "")
	cfg := defaultConfig()
	cfg.AutoUpdate = false
	if _, err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if autoUpdateEnabled() {
		t.Error("configure --auto-update=false left it on")
	}
}

func TestChecksumFor(t *testing.T) {
	sums := []byte("ABC123  tsp_darwin_arm64.tar.gz\nffff  tsp_linux_amd64.tar.gz\n")
	if got := checksumFor(sums, "tsp_darwin_arm64.tar.gz"); got != "abc123" {
		t.Errorf("got %q", got)
	}
	if got := checksumFor(sums, "tsp_windows_amd64.zip"); got != "" {
		t.Errorf("got %q for a missing name", got)
	}
}

func TestServiceUnit_launcherRevision(t *testing.T) {
	for _, kind := range []string{"launchd", "systemd"} {
		s, _ := testService(t, kind)
		if !strings.Contains(s.unit(testUser), "TSP_LAUNCHER_REVISION") {
			t.Errorf("%s unit has no launcher revision", kind)
		}
		_ = os.WriteFile(s.unitPath, []byte(s.unit(testUser)), 0o644)
		if !s.selfUpdating() {
			t.Errorf("%s: a launcher unit is not self-updating", kind)
		}
		_ = os.WriteFile(s.unitPath, []byte("<plist>proxy --uid 501</plist>"), 0o644)
		if s.selfUpdating() || s.current(testUser) {
			t.Errorf("%s: a pre-launcher unit counts as current", kind)
		}
	}
}

func TestSetupLocalHTTPS_newerTspNeedsNoSudo(t *testing.T) {
	unixOnly(t)
	home, _ := isolate(t)
	t.Setenv("SSH_CONNECTION", "1")
	withVersion(t, "1.13.0")
	ran := pinSystem(t, 501, &recRunner{})
	dir := filepath.Join(home, ".tailscale-proxy")
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	s, src := testService(t, "launchd")
	_ = copyExecutable(src, s.binPath)
	_ = os.WriteFile(s.unitPath, []byte(s.unit(serviceSpec{uid: os.Getuid(), gid: os.Getgid(), home: home})), 0o644)
	// The proxy of the previous release runs, and restarts on the copy once it lands.
	state := func(v string) {
		data, _ := json.Marshal(proxyState{PID: 7, Version: v, Port: 443, Launcher: "1.12.0"})
		_ = writeFileAtomic(proxyStatePath(dir), data, 0o644)
	}
	state("1.12.0")
	release, _, _ := tryLock(proxyLockPath(dir))
	defer release()
	go func() {
		for proxyBinVersion(dir) != "1.13.0" {
			time.Sleep(5 * time.Millisecond)
		}
		state("1.13.0")
	}()
	out := captureStdout(t, func() {
		if code := setupLocalHTTPS(s, src); code != 0 && runtime.GOOS == "darwin" { // Linux: the stubbed trust leaves the CA untrusted
			t.Errorf("code = %d", code)
		}
	})
	for _, c := range *ran {
		if c[0] == "sudo" && slices.Contains(c, "service") { // Linux trusts the CA through sudo too
			t.Errorf("sudo ran for an update: %v", c)
		}
	}
	if !strings.Contains(out, "→ the local HTTPS proxy moves from tsp 1.12.0 to 1.13.0 (no sudo)") || !strings.Contains(out, "✓ local HTTPS proxy running (tsp 1.13.0, pid 7, port 443); it updates itself") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSetupLocalHTTPS_oldDaemonMigratesOnce(t *testing.T) {
	unixOnly(t)
	home, _ := isolate(t)
	t.Setenv("SSH_CONNECTION", "1")
	ran := pinSystem(t, 501, &recRunner{})
	dir := filepath.Join(home, ".tailscale-proxy")
	sudoStartsProxy(t, dir, ran)
	s, src := testService(t, "launchd")
	_ = copyExecutable(src, s.binPath)
	_ = os.WriteFile(s.unitPath, []byte("<plist>the 1.12 daemon</plist>"), 0o644)
	out := captureStdout(t, func() { setupLocalHTTPS(s, src) })
	if len(*ran) == 0 || (*ran)[0][0] != "sudo" {
		t.Fatalf("ran %v, want the one sudo of the migration", *ran)
	}
	if !strings.Contains(out, "→ moving the boot service to the self-updating launcher (once: later updates need no sudo)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestLocalHTTPSChecks_selfUpdatingNotes(t *testing.T) {
	home, _ := isolate(t)
	withVersion(t, "1.13.0")
	t.Setenv("TSP_NO_UPDATE", "")
	t.Setenv("CI", "")
	dir := filepath.Join(home, ".tailscale-proxy")
	_ = os.MkdirAll(dir, 0o755)
	data, _ := json.Marshal(proxyState{PID: 42, Version: "1.12.0", Port: 443})
	_ = os.WriteFile(proxyStatePath(dir), data, 0o644)
	release, _, _ := tryLock(proxyLockPath(dir))
	defer release()
	saveUpdateState(dir, updateState{CheckedAt: time.Now().Add(-2 * time.Hour), Latest: "v1.13.0"})
	s, _ := testService(t, "launchd")
	_ = os.WriteFile(s.unitPath, []byte(s.unit(testUser)), 0o644)
	checks, err := localHTTPSChecks(s, true)
	if err != nil {
		t.Fatal(err)
	}
	var names, notes []string
	for _, c := range checks {
		names, notes = append(names, c.Name), append(notes, c.Note)
	}
	all := strings.Join(names, "\n") + "\n" + strings.Join(notes, "\n")
	if !strings.Contains(all, "it restarts on "+proxyBinPath(dir)+" within seconds") || !strings.Contains(all, "auto-update on (checked 2h0m0s ago, latest 1.13.0)") {
		t.Errorf("checks:\n%s", all)
	}
}
