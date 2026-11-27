package core

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// recRunner records every command and fails those listed in fail.
type recRunner struct {
	calls []string
	fail  map[string]bool
}

func (r *recRunner) Run(name string, args ...string) (string, string, error) {
	c := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, c)
	if r.fail[c] {
		return "", "boom", errString("exit 1")
	}
	return "", "", nil
}

func testService(t *testing.T, kind string) (serviceSystem, string) {
	t.Helper()
	orig := checkRootOnlyPath
	checkRootOnlyPath = func(string) error { return nil } // temp dirs belong to the user
	t.Cleanup(func() { checkRootOnlyPath = orig })
	dir := t.TempDir()
	src := filepath.Join(dir, "tsp-src")
	_ = os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755)
	return serviceSystem{
		kind:     kind,
		unitPath: filepath.Join(dir, "unit"),
		binPath:  filepath.Join(dir, "lib", "tailscale-proxy", "tsp"),
		logPath:  "/var/log/tailscale-proxy.log",
	}, src
}

var testUser = serviceSpec{uid: 501, gid: 20, home: "/Users/me & co"}

func TestServiceUnit_launchd(t *testing.T) {
	s, _ := testService(t, "launchd")
	u := s.unit(testUser)
	for _, want := range []string{
		"<string>" + serviceLabel + "</string>",
		"<string>" + s.binPath + "</string>",
		"<string>proxy</string>", "<string>--uid</string>", "<string>501</string>", "<string>20</string>",
		"<string>/Users/me &amp; co</string>",
		"<key>KeepAlive</key>", "<key>RunAtLoad</key>", "/var/log/tailscale-proxy.log",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("plist lacks %q:\n%s", want, u)
		}
	}
}

func TestServiceUnit_systemd(t *testing.T) {
	s, _ := testService(t, "systemd")
	u := s.unit(serviceSpec{uid: 1000, gid: 1000, home: "/home/me"})
	want := `ExecStart=` + systemdQuote(s.binPath) + ` "proxy" "--uid" "1000" "--gid" "1000" "--home" "/home/me"`
	if !strings.Contains(u, want) || !strings.Contains(u, "Restart=always") || !strings.Contains(u, "WantedBy=multi-user.target") {
		t.Errorf("unit:\n%s\nwant %q", u, want)
	}
}

// launchdSim is launchctl with state: print fails while the job is unloaded,
// and the first failBootstraps bootstraps fail like a job still tearing down.
type launchdSim struct {
	recRunner
	loaded         bool
	failBootstraps int
}

func (l *launchdSim) Run(name string, args ...string) (string, string, error) {
	_, _, _ = l.recRunner.Run(name, args...)
	switch args[0] {
	case "print":
		if !l.loaded {
			return "", "not found", errString("exit 113")
		}
	case "bootout":
		l.loaded = false
	case "bootstrap":
		if l.failBootstraps > 0 {
			l.failBootstraps--
			return "", "Bootstrap failed: 5: Input/output error", errString("exit 5")
		}
		l.loaded = true
	}
	return "", "", nil
}

func fastRetry(t *testing.T) {
	orig := serviceRetry
	serviceRetry = time.Millisecond
	t.Cleanup(func() { serviceRetry = orig })
}

func TestServiceInstallUninstall_launchd(t *testing.T) {
	fastRetry(t)
	s, src := testService(t, "launchd")
	target := "system/" + serviceLabel
	r := &launchdSim{}
	if err := s.install(r, src, testUser); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(s.binPath); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o755) {
		t.Errorf("binary %v %v, want 0755", fi, err)
	}
	if !s.installed() || !s.current(src, testUser) {
		t.Error("not installed and current after install")
	}
	if got := strings.Join(r.calls, "; "); got != "launchctl enable "+target+"; launchctl print "+target+"; launchctl bootstrap system "+s.unitPath {
		t.Errorf("first install = %s", got)
	}

	r.calls = nil // the same install again: restarted in place, nothing unloaded
	if err := s.install(r, src, testUser); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.calls, "; "); got != "launchctl enable "+target+"; launchctl print "+target+"; launchctl kickstart -k "+target {
		t.Errorf("unchanged install = %s", got)
	}

	r.calls, r.failBootstraps = nil, 2 // a new plist: unloaded, then loaded once launchd lets go
	other := serviceSpec{uid: 502, gid: 20, home: "/Users/other"}
	if err := s.install(r, src, other); err != nil {
		t.Fatalf("install through two failed bootstraps: %v", err)
	}
	if got := strings.Count(strings.Join(r.calls, "; "), "bootstrap"); got != 3 || !r.loaded || !s.current(src, other) {
		t.Errorf("bootstraps = %d, loaded = %v: %v", got, r.loaded, r.calls)
	}

	r.calls = nil
	if err := s.uninstall(r); err != nil {
		t.Fatal(err)
	}
	if s.installed() {
		t.Error("installed() = true after uninstall")
	}
	if _, err := os.Stat(filepath.Dir(s.binPath)); !os.IsNotExist(err) {
		t.Error("the binary's folder was left behind")
	}
	if err := s.uninstall(r); err != nil {
		t.Errorf("a second uninstall failed: %v", err)
	}
}

func TestServiceInstall_launchdGivesUp(t *testing.T) {
	fastRetry(t)
	s, src := testService(t, "launchd")
	err := s.install(&launchdSim{failBootstraps: 99}, src, testUser)
	if err == nil || !strings.Contains(err.Error(), "Input/output error") {
		t.Errorf("err = %v, want the last bootstrap error", err)
	}
}

func TestServiceInstall_launchdLoadedMeanwhile(t *testing.T) {
	// A bootstrap that fails because another install loaded the job: restart that one.
	fastRetry(t)
	s, src := testService(t, "launchd")
	r := &racedSim{}
	if err := s.install(r, src, testUser); err != nil {
		t.Fatal(err)
	}
	if got := r.calls[len(r.calls)-1]; got != "launchctl kickstart -k system/"+serviceLabel {
		t.Errorf("last command = %s", got)
	}
}

type racedSim struct{ launchdSim }

func (r *racedSim) Run(name string, args ...string) (string, string, error) {
	if args[0] == "bootstrap" {
		_, _, _ = r.recRunner.Run(name, args...)
		r.loaded = true
		return "", "service already loaded", errString("exit 5")
	}
	return r.launchdSim.Run(name, args...)
}

func TestServiceInstallUninstall_systemd(t *testing.T) {
	s, src := testService(t, "systemd")
	r := &recRunner{}
	if err := s.install(r, src, testUser); err != nil {
		t.Fatal(err)
	}
	want := "systemctl daemon-reload; systemctl enable tailscale-proxy.service; systemctl restart tailscale-proxy.service"
	if got := strings.Join(r.calls, "; "); got != want {
		t.Errorf("commands = %s", got)
	}
	r.calls = nil
	_ = s.install(r, src, testUser) // unchanged unit: no reload
	if got := strings.Join(r.calls, "; "); got != "systemctl enable tailscale-proxy.service; systemctl restart tailscale-proxy.service" {
		t.Errorf("unchanged install = %s", got)
	}
	r.calls = nil
	_ = s.uninstall(r)
	if got := strings.Join(r.calls, "; "); got != "systemctl disable --now tailscale-proxy.service; systemctl daemon-reload" {
		t.Errorf("uninstall commands = %s", got)
	}
}

func TestServiceInstall_errors(t *testing.T) {
	s, src := testService(t, "systemd")
	r := &recRunner{fail: map[string]bool{"systemctl enable tailscale-proxy.service": true}}
	err := s.install(r, src, testUser)
	if err == nil || !strings.Contains(err.Error(), "systemctl enable") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want the failing command and its stderr", err)
	}
	if err := s.install(&recRunner{}, filepath.Join(t.TempDir(), "missing"), testUser); err == nil {
		t.Error("install from a missing binary succeeded")
	}
	s.unitPath = filepath.Join(t.TempDir(), "no", "such", "dir", "unit")
	if err := s.install(&recRunner{}, src, testUser); err == nil {
		t.Error("install into a missing unit dir succeeded")
	}
}

func TestServiceUser(t *testing.T) {
	sp, err := serviceUser(-1, -1, "", env(map[string]string{"SUDO_UID": "501", "SUDO_GID": "20", "HOME": "/Users/me"}))
	if err != nil || sp != (serviceSpec{uid: 501, gid: 20, home: "/Users/me"}) {
		t.Errorf("from sudo: %+v, %v", sp, err)
	}
	sp, err = serviceUser(1000, 1000, "/home/me", env(nil))
	if err != nil || sp.home != "/home/me" {
		t.Errorf("from flags: %+v, %v", sp, err)
	}
	for name, m := range map[string]map[string]string{
		"no sudo uid": {},
		"root uid":    {"SUDO_UID": "0"},
		"no gid":      {"SUDO_UID": "501"},
		"root home":   {"SUDO_UID": "501", "SUDO_GID": "20", "HOME": "/var/root"},
		"no home":     {"SUDO_UID": "501", "SUDO_GID": "20"},
	} {
		if _, err := serviceUser(-1, -1, "", env(m)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSystemdQuote(t *testing.T) {
	if got := systemdQuote(`/home/a b/%h "x" \y`); got != `"/home/a b/%%h \"x\" \\y"` {
		t.Errorf("systemdQuote = %s", got)
	}
}

func TestServiceInstall_refusesAUserWritablePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the boot service (and its root-only check) is macOS/Linux only")
	}
	s, src := testService(t, "launchd")
	checkRootOnlyPath = checkRootOnly // the real check: the temp dir is ours, not root's
	if err := s.install(&recRunner{}, src, testUser); err == nil || !strings.Contains(err.Error(), "must belong to root") {
		t.Errorf("err = %v, want a refusal", err)
	}
	if _, err := os.Stat(s.binPath); !os.IsNotExist(err) {
		t.Error("the binary was copied into a user-writable folder")
	}
}

func TestXMLEscape(t *testing.T) {
	if xmlEscape("a<b>&c") != "a&lt;b&gt;&amp;c" {
		t.Error("xmlEscape")
	}
}

func TestCmdService_usage(t *testing.T) {
	if code := cmdService(nil); code != 2 {
		t.Errorf("no subcommand = %d, want 2", code)
	}
	if code := cmdService([]string{"--help"}); code != 0 {
		t.Errorf("--help = %d, want 0", code)
	}
	if code := cmdService([]string{"bogus"}); code != 2 {
		t.Errorf("unknown subcommand = %d, want 2", code)
	}
}

// captureStdout returns what f prints to stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	out := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		out <- string(data)
	}()
	f()
	_ = w.Close()
	return <-out
}

func TestCmdServiceStatus_notSetUp(t *testing.T) {
	isolate(t)
	s, _ := testService(t, "launchd")
	code := 0
	out := captureStdout(t, func() { code = cmdServiceStatus(s, true) })
	if code != 1 {
		t.Errorf("status with nothing installed = %d, want 1", code)
	}
	if !strings.Contains(out, "✗ local HTTPS proxy not running\n    → tsp service install") || strings.Contains(out, "pid 0") {
		t.Errorf("status output:\n%s", out)
	}
}

func TestCmdServiceStatus_proxyRunning(t *testing.T) {
	home, _ := isolate(t)
	dir := filepath.Join(home, ".tailscale-proxy")
	_ = os.MkdirAll(dir, 0o755)
	data, _ := json.Marshal(proxyState{PID: 42, Version: Version, Port: 443})
	_ = os.WriteFile(proxyStatePath(dir), data, 0o644)
	release, _, _ := tryLock(proxyLockPath(dir))
	defer release()
	s, _ := testService(t, "launchd")
	out := captureStdout(t, func() { cmdServiceStatus(s, true) })
	if !strings.Contains(out, "\n✓ proxy running (tsp "+Version+", pid 42, port 443)\n") {
		t.Errorf("status output:\n%s", out)
	}
}

// pinSystem replaces the root check, terminal commands and service Runner.
func pinSystem(t *testing.T, euid int, r Runner) *[][]string {
	t.Helper()
	ran := &[][]string{}
	origE, origI, origR, origL := geteuid, interactive, serviceRunner, lookPath
	geteuid = func() int { return euid }
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil } // sudo is there, even in a container
	interactive = func(argv []string) error { *ran = append(*ran, argv); return nil }
	serviceRunner = r
	t.Cleanup(func() { geteuid, interactive, serviceRunner, lookPath = origE, origI, origR, origL })
	return ran
}

// sudoStartsProxy stands in for sudo: the "installed" service's proxy comes up
// for the isolated home, as launchd/systemd would start it.
func sudoStartsProxy(t *testing.T, dir string, ran *[][]string) {
	interactive = func(argv []string) error {
		*ran = append(*ran, argv)
		if argv[0] != "sudo" || !slices.Contains(argv, "service") { // Linux: the trust steps run through sudo too
			return nil
		}
		data, _ := json.Marshal(proxyState{PID: 42, Version: Version, Port: 443})
		_ = os.WriteFile(proxyStatePath(dir), data, 0o644)
		release, _, _ := tryLock(proxyLockPath(dir))
		t.Cleanup(release)
		return nil
	}
}

func unixOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the boot service is macOS/Linux only")
	}
}

func TestCmdServiceInstall_setsUpAsTheCaller(t *testing.T) {
	unixOnly(t)
	home, _ := isolate(t)
	t.Setenv("SSH_CONNECTION", "1") // macOS: leave the keychain dialog as a next step
	ran := pinSystem(t, 501, &recRunner{})
	dir := filepath.Join(home, ".tailscale-proxy")
	sudoStartsProxy(t, dir, ran)
	s, _ := testService(t, "launchd")
	code := 0
	out := captureStdout(t, func() { code = cmdServiceInstall(s, nil) })
	if len(*ran) == 0 || (*ran)[0][0] != "sudo" {
		t.Fatalf("ran %v, want sudo first", *ran)
	}
	got := strings.Join((*ran)[0], " ")
	if !strings.Contains(got, " service install --uid ") || !strings.HasSuffix(got, "--home "+home) {
		t.Errorf("sudo argv = %s", got)
	}
	if !strings.Contains(out, "→ installing the local HTTPS proxy as a boot service") || !strings.Contains(out, "✓ local HTTPS proxy running (tsp "+Version+", pid 42") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ca", "ca.pem")); err != nil {
		t.Error("the CA was not made as the caller before sudo")
	}
	if s.installed() {
		t.Error("the non-root process wrote the unit itself")
	}
	if runtime.GOOS == "darwin" && code != 0 {
		t.Errorf("code = %d, want 0 (trust left for the Mac's screen)", code)
	}
}

func TestCmdServiceInstall_upToDateNeedsNoSudo(t *testing.T) {
	unixOnly(t)
	home, _ := isolate(t)
	t.Setenv("SSH_CONNECTION", "1")
	ran := pinSystem(t, 501, &recRunner{})
	dir := filepath.Join(home, ".tailscale-proxy")
	_ = os.MkdirAll(dir, 0o755)
	data, _ := json.Marshal(proxyState{PID: 7, Version: Version, Port: 443})
	_ = os.WriteFile(proxyStatePath(dir), data, 0o644)
	release, _, _ := tryLock(proxyLockPath(dir))
	defer release()
	s, _ := testService(t, "launchd")
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	_ = copyExecutable(self, s.binPath)
	_ = os.WriteFile(s.unitPath, []byte(s.unit(serviceSpec{uid: os.Getuid(), gid: os.Getgid(), home: home})), 0o644)
	out := captureStdout(t, func() { cmdServiceInstall(s, nil) })
	for _, c := range *ran {
		if c[0] == "sudo" && slices.Contains(c, "service") { // Linux trusts the CA through sudo too
			t.Errorf("the boot service was reinstalled although everything is current: %v", c)
		}
	}
	if !strings.Contains(out, "✓ local HTTPS proxy up to date and running (tsp "+Version+", pid 7, port 443)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSetupLocalHTTPS_proxyDoesNotStart(t *testing.T) {
	unixOnly(t)
	isolate(t)
	pinSystem(t, 501, &recRunner{})
	orig := proxyStartWait
	proxyStartWait = 10 * time.Millisecond
	t.Cleanup(func() { proxyStartWait = orig })
	s, src := testService(t, "launchd")
	s.logPath = filepath.Join(t.TempDir(), "proxy.log")
	_ = os.WriteFile(s.logPath, []byte("cannot listen on 127.0.0.1:443: address already in use\n"), 0o644)
	stderr := captureStderr(t, func() {
		if code := setupLocalHTTPS(s, src); code != 1 {
			t.Errorf("code = %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "address already in use") || !strings.Contains(stderr, "lsof -nP -iTCP:443") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestSetupReason(t *testing.T) {
	s, src := testService(t, "launchd")
	if got := setupReason(s, src, testUser, proxyState{}, false); !strings.HasPrefix(got, "installing") {
		t.Errorf("not installed: %s", got)
	}
	_ = copyExecutable(src, s.binPath)
	_ = os.WriteFile(s.unitPath, []byte("old"), 0o644)
	if got := setupReason(s, src, testUser, proxyState{}, false); !strings.HasPrefix(got, "updating") {
		t.Errorf("old unit: %s", got)
	}
	_ = os.WriteFile(s.unitPath, []byte(s.unit(testUser)), 0o644)
	if got := setupReason(s, src, testUser, proxyState{Version: "0.0.1"}, true); !strings.Contains(got, "it runs 0.0.1") && Version != "dev" {
		t.Errorf("old proxy: %s", got)
	}
	if got := setupReason(s, src, testUser, proxyState{}, false); !strings.Contains(got, "not running") {
		t.Errorf("stopped: %s", got)
	}
}

func TestElevate_withoutSudo(t *testing.T) {
	pinSystem(t, 501, &recRunner{})
	lookPath = func(string) (string, error) { return "", errString("not found") }
	stderr := captureStderr(t, func() {
		if code, done := elevate([]string{"uninstall"}, "removing the boot service"); code != 1 || !done {
			t.Errorf("elevate = %d, %v", code, done)
		}
	})
	if !strings.Contains(stderr, "sudo is not installed: run as root") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// captureStderr returns what f prints to stderr.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	out := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		out <- string(data)
	}()
	f()
	_ = w.Close()
	return <-out
}

func TestCmdServiceInstall_asRoot(t *testing.T) {
	isolate(t)
	t.Setenv("SUDO_UID", "") // also under `sudo go test`
	t.Setenv("SUDO_GID", "")
	r := &recRunner{}
	pinSystem(t, 0, r)
	s, _ := testService(t, "systemd")
	if code := cmdServiceInstall(s, []string{"--uid", "1000", "--gid", "1000", "--home", "/home/me"}); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !s.installed() || len(r.calls) != 3 { // daemon-reload, enable, restart
		t.Errorf("installed=%v calls=%v", s.installed(), r.calls)
	}
	if code := cmdServiceInstall(s, nil); code != 2 {
		t.Errorf("root without a caller = %d, want 2", code)
	}
	if code := cmdServiceInstall(s, []string{"--bogus"}); code != 2 {
		t.Errorf("bad flag = %d, want 2", code)
	}
	r.fail = map[string]bool{"systemctl restart tailscale-proxy.service": true}
	if code := cmdServiceInstall(s, []string{"--uid", "1000", "--gid", "1000", "--home", "/home/me"}); code != 1 {
		t.Errorf("failing systemctl = %d, want 1", code)
	}
}

func TestCmdService_uninstall(t *testing.T) {
	// Never as root here: that would remove the machine's real service files.
	isolate(t)
	ran := pinSystem(t, 501, &recRunner{})
	s, _ := testService(t, "launchd")
	orig := servicePlatform
	servicePlatform = func() (serviceSystem, bool) { return s, true }
	t.Cleanup(func() { servicePlatform = orig })
	out := captureStdout(t, func() {
		if code := cmdService([]string{"uninstall"}); code != 0 {
			t.Errorf("nothing installed = %d, want 0", code)
		}
	})
	if len(*ran) != 0 || !strings.Contains(out, "nothing to remove") {
		t.Errorf("ran %v, output %q", *ran, out)
	}
	_ = os.WriteFile(s.unitPath, []byte("x"), 0o644)
	interactive = func([]string) error { return errString("no tty") }
	if code := cmdService([]string{"uninstall"}); code != 1 {
		t.Errorf("sudo failing = %d, want 1", code)
	}
}

func TestHealProxy_outsideATerminalOnlyWarns(t *testing.T) {
	home, _ := isolate(t)
	stderr := captureStderr(t, func() { healProxy(filepath.Join(home, ".tailscale-proxy"), localRoute{Name: "api"}) })
	if !strings.Contains(stderr, "https://api.localhost will not answer: tsp service install") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestCmdService_status(t *testing.T) {
	isolate(t)
	if code := cmdService([]string{"status"}); code != 1 {
		t.Errorf("status with nothing set up = %d, want 1", code)
	}
}
