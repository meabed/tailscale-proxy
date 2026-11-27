package core

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// `tsp service install|uninstall|status` keeps `tsp proxy` running from boot.
// The service runs a root-owned copy of this binary (a root process never runs
// a file its user could replace) that binds 443, then drops to that user.

const serviceLabel = "com.meabed.tailscale-proxy"

// checkRootOnlyPath is checkRootOnly, replaceable in tests (their temp dirs are the user's).
var checkRootOnlyPath = checkRootOnly

// serviceSystem is one init system: where its files go and how to (re)load them.
// The platform's is platformService (service_*.go); tests build their own.
type serviceSystem struct {
	kind     string // "launchd" | "systemd"
	unitPath string // plist / unit file
	binPath  string // root-owned copy of tsp
	logPath  string // launchd only (systemd logs to the journal)
}

// Seams for tests: who we run as, and commands that need the terminal (sudo, a
// password dialog) instead of captured output.
var (
	geteuid     = os.Geteuid
	lookPath    = exec.LookPath
	interactive = func(argv []string) error {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	serviceRunner   Runner = execRunner{}
	servicePlatform        = platformService
)

// serviceSpec is the user the proxy serves.
type serviceSpec struct {
	uid, gid int
	home     string
}

// unit renders the launchd plist or systemd unit.
func (s serviceSystem) unit(sp serviceSpec) string {
	args := []string{s.binPath, "proxy", "--uid", strconv.Itoa(sp.uid), "--gid", strconv.Itoa(sp.gid), "--home", sp.home}
	if s.kind == "systemd" {
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = systemdQuote(a)
		}
		return `[Unit]
Description=tailscale-proxy: https://*.localhost for tsp run
After=network.target

[Service]
ExecStart=` + strings.Join(quoted, " ") + `
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
`
	}
	var b strings.Builder
	for _, a := range args {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + serviceLabel + `</string>
	<key>ProgramArguments</key>
	<array>
` + b.String() + `	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardErrorPath</key>
	<string>` + s.logPath + `</string>
</dict>
</plist>
`
}

// systemdQuote quotes one ExecStart argument ("…", with \\, " and % escaped).
func systemdQuote(a string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(a) + `"`
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// install brings the service in line with self and sp, changing only what
// differs: the binary, the unit, then a restart so the new binary runs.
func (s serviceSystem) install(r Runner, self string, sp serviceSpec) error {
	if err := checkRootOnlyPath(s.binPath); err != nil {
		return err
	}
	if !sameFile(self, s.binPath) {
		if err := copyExecutable(self, s.binPath); err != nil {
			return fmt.Errorf("copying tsp to %s: %v", s.binPath, err)
		}
	}
	unit := s.unit(sp)
	old, _ := os.ReadFile(s.unitPath)
	changed := string(old) != unit
	if changed {
		if err := os.WriteFile(s.unitPath, []byte(unit), 0o644); err != nil {
			return fmt.Errorf("writing %s: %v", s.unitPath, err)
		}
	}
	if s.kind == "systemd" {
		cmds := [][]string{{"systemctl", "enable", "tailscale-proxy.service"}, {"systemctl", "restart", "tailscale-proxy.service"}}
		if changed {
			cmds = append([][]string{{"systemctl", "daemon-reload"}}, cmds...)
		}
		return runAll(r, cmds)
	}
	return s.launchdLoad(r, changed)
}

// serviceRetry is the pause between launchd checks, shortened in tests.
var serviceRetry = 100 * time.Millisecond

// launchdLoad (re)starts the daemon. A loaded daemon with an unchanged plist is
// restarted in place (kickstart -k runs the new binary). Otherwise the old job
// is unloaded and the plist loaded once launchd has let the old job go: a
// bootstrap right after bootout fails with "5: Input/output error".
func (s serviceSystem) launchdLoad(r Runner, changed bool) error {
	target := "system/" + serviceLabel
	loaded := func() bool { _, _, err := r.Run("launchctl", "print", target); return err == nil }
	kickstart := [][]string{{"launchctl", "kickstart", "-k", target}}
	_, _, _ = r.Run("launchctl", "enable", target) // undo a `launchctl disable`, which blocks bootstrap
	if loaded() {
		if !changed {
			return runAll(r, kickstart)
		}
		_, _, _ = r.Run("launchctl", "bootout", target)
		for i := 0; i < 100 && loaded(); i++ {
			time.Sleep(serviceRetry)
		}
	}
	var err error
	for i := 0; i < 5; i++ {
		if err = runAll(r, [][]string{{"launchctl", "bootstrap", "system", s.unitPath}}); err == nil {
			return nil
		}
		if loaded() { // loaded by a concurrent install: restart it on the new binary
			return runAll(r, kickstart)
		}
		time.Sleep(10 * serviceRetry)
	}
	return err
}

// uninstall stops the service and removes its unit and binary.
func (s serviceSystem) uninstall(r Runner) error {
	if s.kind == "systemd" {
		_, _, _ = r.Run("systemctl", "disable", "--now", "tailscale-proxy.service")
	} else {
		_, _, _ = r.Run("launchctl", "bootout", "system/"+serviceLabel)
	}
	for _, p := range []string{s.unitPath, s.binPath} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if filepath.Base(filepath.Dir(s.binPath)) == "tailscale-proxy" {
		_ = os.Remove(filepath.Dir(s.binPath)) // our own folder only, never a shared one
	}
	if s.kind == "systemd" {
		_, _, _ = r.Run("systemctl", "daemon-reload")
	}
	return nil
}

// current reports whether the installed service already runs self for sp.
func (s serviceSystem) current(self string, sp serviceSpec) bool {
	unit, err := os.ReadFile(s.unitPath)
	return err == nil && string(unit) == s.unit(sp) && sameFile(self, s.binPath)
}

func sameFile(a, b string) bool {
	da, errA := os.ReadFile(a)
	db, errB := os.ReadFile(b)
	return errA == nil && errB == nil && bytes.Equal(da, db)
}

func (s serviceSystem) installed() bool {
	_, err := os.Stat(s.unitPath)
	return err == nil
}

func runAll(r Runner, cmds [][]string) error {
	for _, c := range cmds {
		if _, stderr, err := r.Run(c[0], c[1:]...); err != nil {
			return fmt.Errorf("%s: %v %s", strings.Join(c, " "), err, strings.TrimSpace(stderr))
		}
	}
	return nil
}

// copyExecutable installs src at dst (0755) atomically, so a running service
// never sees a half-written binary.
func copyExecutable(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(dst, data, 0o755)
}

func serviceUsage() {
	fmt.Print(`tsp service — run the local HTTPS proxy (https://*.localhost) from boot

Usage:
  tsp service install     set it up or update it: the boot service, then the CA's trust
  tsp service uninstall   stop and remove it
  tsp service status      installed, running, and trusted? (exit 1 if not)

install checks what is in place first and changes only what is missing or old:
sudo asks for your password only when the boot service itself must change.
Run it again any time (after updating tsp, after a failed start): it is safe.
`)
}

func cmdService(argv []string) int {
	if len(argv) == 0 {
		serviceUsage()
		return 2
	}
	s, supported := servicePlatform()
	switch argv[0] {
	case "-h", "--help":
		serviceUsage()
		return 0
	case "status":
		return cmdServiceStatus(s, supported)
	case "install", "uninstall":
	default:
		serviceUsage()
		return 2
	}
	if !supported {
		fmt.Fprintf(os.Stderr, "tsp service supports macOS (launchd) and Linux (systemd), not %s: run `tsp proxy --port 8443` in a terminal instead\n", runtime.GOOS)
		return 1
	}
	if argv[0] == "install" {
		return cmdServiceInstall(s, argv[1:])
	}
	if geteuid() != 0 && !s.installed() {
		fmt.Println("✓ the local HTTPS proxy is not installed: nothing to remove")
		return 0
	}
	if code, done := elevate(argv, "removing the boot service"); done {
		return code
	}
	if err := s.uninstall(serviceRunner); err != nil {
		fmt.Fprintln(os.Stderr, "tsp service uninstall:", err)
		return 1
	}
	fmt.Println("✓ local HTTPS proxy removed (the CA stays in ~/.tailscale-proxy/ca; tsp trust added it to the OS trust store)")
	return 0
}

func cmdServiceInstall(s serviceSystem, argv []string) int {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	uid := fs.Int("uid", -1, "user the proxy runs as (default: the sudo caller)")
	gid := fs.Int("gid", -1, "that user's group")
	home := fs.String("home", "", "that user's home")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp service install: locating tsp:", err)
		return 1
	}
	if geteuid() != 0 && *uid < 0 {
		return setupLocalHTTPS(s, self)
	}
	if code, done := elevate(append([]string{"install"}, argv...), "installing the boot service"); done {
		return code
	}
	sp, err := serviceUser(*uid, *gid, *home, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp service install:", err)
		return 2
	}
	if err := s.install(serviceRunner, self, sp); err != nil {
		fmt.Fprintln(os.Stderr, "tsp service install:", err)
		return 1
	}
	fmt.Printf("✓ boot service %s runs tsp %s for uid %d\n", s.unitPath, Version, sp.uid)
	return 0
}

// setupLocalHTTPS makes https://*.localhost work for the calling user: the CA
// (made as the user, so its files are theirs), the boot service when it is
// missing, old or not running (the one step that needs sudo), then the trust.
func setupLocalHTTPS(s serviceSystem, self string) int {
	dir, err := stateDir()
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp service install:", err)
		return 1
	}
	ca, err := ensureCA(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp service install:", err)
		return 1
	}
	release, _, _ := waitLock(filepath.Join(dir, "service.lock"), 2*time.Minute) // one setup at a time (parallel `tsp run`s)
	if release != nil {
		defer release()
	}
	home, _ := os.UserHomeDir()
	sp := serviceSpec{uid: os.Getuid(), gid: os.Getgid(), home: home}
	st, running := runningProxy(dir)
	switch {
	case running && !st.outdated() && s.current(self, sp):
		fmt.Printf("✓ local HTTPS proxy up to date and running (tsp %s, pid %d, port %d)\n", st.Version, st.PID, st.Port)
	default:
		fmt.Println("→ " + setupReason(s, self, sp, st, running))
		if code, _ := elevate([]string{"install", "--uid", strconv.Itoa(sp.uid), "--gid", strconv.Itoa(sp.gid), "--home", sp.home}, "installing the boot service (port 443)"); code != 0 {
			return code
		}
		if st, ok := waitProxy(dir, proxyStartWait); ok {
			fmt.Printf("✓ local HTTPS proxy running (tsp %s, pid %d, port %d)\n", st.Version, st.PID, st.Port)
		} else {
			explainProxyDown(s)
			return 1
		}
	}
	return trustCA(ca, true)
}

// setupReason says what install is about to change, before sudo asks.
func setupReason(s serviceSystem, self string, sp serviceSpec, st proxyState, running bool) string {
	switch {
	case !s.installed():
		return "installing the local HTTPS proxy as a boot service"
	case !s.current(self, sp):
		return "updating the boot service to tsp " + Version
	case running && st.outdated():
		return fmt.Sprintf("restarting the proxy on tsp %s (it runs %s)", Version, st.Version)
	}
	return "restarting the boot service (the proxy is not running)"
}

// proxyStartWait bounds how long install waits for the restarted proxy.
var proxyStartWait = 10 * time.Second

// waitProxy waits for the proxy of this tsp version to come up.
func waitProxy(dir string, d time.Duration) (proxyState, bool) {
	for deadline := time.Now().Add(d); ; time.Sleep(200 * time.Millisecond) {
		if st, ok := runningProxy(dir); ok && !st.outdated() {
			return st, true
		}
		if time.Now().After(deadline) {
			return proxyState{}, false
		}
	}
}

// explainProxyDown prints why a freshly installed proxy did not start, from its log.
func explainProxyDown(s serviceSystem) {
	fmt.Fprintln(os.Stderr, "✗ the boot service is installed, but the proxy did not start")
	if s.kind == "systemd" {
		fmt.Fprintln(os.Stderr, "    → its log: journalctl -u tailscale-proxy -n 30")
	} else if data, err := os.ReadFile(s.logPath); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		fmt.Fprintln(os.Stderr, "    last lines of "+s.logPath+":")
		for _, l := range lines[max(0, len(lines)-8):] {
			fmt.Fprintln(os.Stderr, "      "+l)
		}
	}
	fmt.Fprintln(os.Stderr, "    → another program on port 443? lsof -nP -iTCP:443 -sTCP:LISTEN, stop it, then tsp service install")
}

// serviceUser resolves who the service serves: explicit flags, else the sudo
// caller (SUDO_UID, SUDO_GID and the HOME it kept).
func serviceUser(uid, gid int, home string, getenv func(string) string) (serviceSpec, error) {
	if uid < 0 {
		uid, _ = strconv.Atoi(getenv("SUDO_UID"))
		if uid <= 0 {
			return serviceSpec{}, fmt.Errorf("run it as your user (it asks for sudo), or pass --uid --gid --home")
		}
	}
	if gid < 0 {
		var err error
		if gid, err = strconv.Atoi(getenv("SUDO_GID")); err != nil {
			return serviceSpec{}, fmt.Errorf("pass --gid (SUDO_GID is not set)")
		}
	}
	if home == "" {
		home = getenv("HOME")
	}
	if home == "" || home == "/root" || home == "/var/root" {
		return serviceSpec{}, fmt.Errorf("pass --home (the user's home, where ~/.tailscale-proxy lives)")
	}
	return serviceSpec{uid: uid, gid: gid, home: home}, nil
}

// elevate re-runs `tsp service <args>` through sudo for a non-root caller.
// done=false: already root, carry on.
func elevate(args []string, why string) (code int, done bool) {
	if geteuid() == 0 {
		return 0, false
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "locating tsp:", err)
		return 1, true
	}
	full := append([]string{self, "service"}, args...)
	if _, err := lookPath("sudo"); err != nil {
		fmt.Fprintf(os.Stderr, "tsp: %s needs root, and sudo is not installed: run as root\n    %s\n", why, strings.Join(full, " "))
		return 1, true
	}
	fmt.Fprintf(os.Stderr, "  %s needs admin rights: sudo may ask for your password\n", why)
	if err := interactive(append([]string{"sudo"}, full...)); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), true
		}
		fmt.Fprintln(os.Stderr, "sudo:", err)
		return 1, true
	}
	return 0, true
}

// cmdServiceStatus prints the proxy's state; exit 0 only when names work in a browser.
func cmdServiceStatus(s serviceSystem, supported bool) int {
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ok := true
	line := func(good bool, text, fix string) {
		if good {
			fmt.Println("✓ " + text)
			return
		}
		ok = false
		fmt.Println("✗ " + text + "\n    → " + fix)
	}
	if supported {
		line(s.installed(), "boot service "+s.unitPath, "tsp service install")
	}
	st, running := runningProxy(dir)
	proxyLine := "local HTTPS proxy not running"
	if running {
		proxyLine = fmt.Sprintf("proxy running (tsp %s, pid %d, port %d)", st.Version, st.PID, st.Port)
	}
	line(running, proxyLine, "tsp service install (or `tsp proxy` in a terminal)")
	if running && st.outdated() {
		fmt.Printf("  ! the proxy runs tsp %s, this is %s: tsp service install updates it\n", st.Version, Version)
	}
	ca, err := loadCA(caFiles(dir))
	line(err == nil && caTrusted(ca), "local CA trusted by the OS", "tsp trust (macOS: in a terminal at the Mac's screen)")
	if !ok {
		return 1
	}
	return 0
}
