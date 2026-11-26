package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	want := `ExecStart="` + s.binPath + `" "proxy" "--uid" "1000" "--gid" "1000" "--home" "/home/me"`
	if !strings.Contains(u, want) || !strings.Contains(u, "Restart=always") || !strings.Contains(u, "WantedBy=multi-user.target") {
		t.Errorf("unit:\n%s\nwant %q", u, want)
	}
}

func TestServiceInstallUninstall_launchd(t *testing.T) {
	s, src := testService(t, "launchd")
	r := &recRunner{fail: map[string]bool{"launchctl bootout system/" + serviceLabel: true}}
	if err := s.install(r, src, testUser); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(s.binPath); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o755) {
		t.Errorf("binary %v %v, want 0755", fi, err)
	}
	if !s.installed() {
		t.Error("installed() = false after install")
	}
	if got := strings.Join(r.calls, "; "); got != "launchctl bootout system/"+serviceLabel+"; launchctl bootstrap system "+s.unitPath {
		t.Errorf("commands = %s", got)
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
	s, src := testService(t, "launchd")
	checkRootOnlyPath = checkRootOnly // the real check: the temp dir is ours, not root's
	if err := s.install(&recRunner{}, src, testUser); err == nil || !strings.Contains(err.Error(), "must belong to root") {
		t.Errorf("err = %v, want a refusal", err)
	}
	if _, err := os.Stat(s.binPath); !os.IsNotExist(err) {
		t.Error("the binary was copied into a user-writable folder")
	}
}

func TestContainsFlagAndXMLEscape(t *testing.T) {
	if !containsFlag([]string{"install", "--uid=5"}, "--uid") || !containsFlag([]string{"--uid", "5"}, "--uid") || containsFlag([]string{"--uidx"}, "--uid") {
		t.Error("containsFlag")
	}
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

func TestCmdServiceStatus_notSetUp(t *testing.T) {
	isolate(t)
	s, _ := testService(t, "launchd")
	if code := cmdServiceStatus(s, true); code != 1 {
		t.Errorf("status with nothing installed = %d, want 1", code)
	}
}

// pinSystem replaces the root check, terminal commands and service Runner.
func pinSystem(t *testing.T, euid int, r Runner) *[][]string {
	t.Helper()
	ran := &[][]string{}
	origE, origI, origR := geteuid, interactive, serviceRunner
	geteuid = func() int { return euid }
	interactive = func(argv []string) error { *ran = append(*ran, argv); return nil }
	serviceRunner = r
	t.Cleanup(func() { geteuid, interactive, serviceRunner = origE, origI, origR })
	return ran
}

func TestCmdServiceInstall_elevatesWithCaller(t *testing.T) {
	home, _ := isolate(t)
	ran := pinSystem(t, 501, &recRunner{})
	s, _ := testService(t, "launchd")
	if code := cmdServiceInstall(s, nil); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if len(*ran) != 1 {
		t.Fatalf("ran %v, want one sudo", *ran)
	}
	got := strings.Join((*ran)[0], " ")
	if !strings.HasPrefix(got, "sudo ") || !strings.Contains(got, " service install --uid ") || !strings.HasSuffix(got, "--home "+home) {
		t.Errorf("sudo argv = %s", got)
	}
	if s.installed() {
		t.Error("the non-root process wrote the unit itself")
	}
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
	if !s.installed() || len(r.calls) != 3 {
		t.Errorf("installed=%v calls=%v", s.installed(), r.calls)
	}
	if code := cmdServiceInstall(s, nil); code != 2 {
		t.Errorf("root without a caller = %d, want 2", code)
	}
	if code := cmdServiceInstall(s, []string{"--bogus"}); code != 2 {
		t.Errorf("bad flag = %d, want 2", code)
	}
	r.fail = map[string]bool{"systemctl daemon-reload": true}
	if code := cmdServiceInstall(s, []string{"--uid", "1000", "--gid", "1000", "--home", "/home/me"}); code != 1 {
		t.Errorf("failing systemctl = %d, want 1", code)
	}
}

func TestCmdService_uninstallSudoFailure(t *testing.T) {
	// Never as root here: that would remove the machine's real service files.
	isolate(t)
	pinSystem(t, 501, &recRunner{})
	interactive = func([]string) error { return errString("no tty") }
	if code := cmdService([]string{"uninstall"}); code != 1 {
		t.Errorf("sudo failing = %d, want 1", code)
	}
}

func TestCmdService_status(t *testing.T) {
	isolate(t)
	if code := cmdService([]string{"status"}); code != 1 {
		t.Errorf("status with nothing set up = %d, want 1", code)
	}
}
