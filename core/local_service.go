package core

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	interactive = func(argv []string) error {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	serviceRunner Runner = execRunner{}
)

// serviceSpec is the user the proxy serves.
type serviceSpec struct {
	uid, gid int
	home     string
}

func (s serviceSystem) proxyArgs(sp serviceSpec) []string {
	return []string{s.binPath, "proxy", "--uid", strconv.Itoa(sp.uid), "--gid", strconv.Itoa(sp.gid), "--home", sp.home}
}

// unit renders the launchd plist or systemd unit.
func (s serviceSystem) unit(sp serviceSpec) string {
	args := s.proxyArgs(sp)
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

// install copies self to binPath, writes the unit and (re)starts the service.
func (s serviceSystem) install(r Runner, self string, sp serviceSpec) error {
	if err := checkRootOnlyPath(s.binPath); err != nil {
		return err
	}
	if err := copyExecutable(self, s.binPath); err != nil {
		return fmt.Errorf("copying tsp to %s: %v", s.binPath, err)
	}
	if err := os.WriteFile(s.unitPath, []byte(s.unit(sp)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %v", s.unitPath, err)
	}
	if s.kind == "systemd" {
		return runAll(r, [][]string{
			{"systemctl", "daemon-reload"},
			{"systemctl", "enable", "tailscale-proxy.service"},
			{"systemctl", "restart", "tailscale-proxy.service"},
		})
	}
	_, _, _ = r.Run("launchctl", "bootout", "system/"+serviceLabel) // not loaded yet: fine
	return runAll(r, [][]string{{"launchctl", "bootstrap", "system", s.unitPath}})
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

// copyExecutable installs src at dst (0755) through a temp file and rename, so
// a running service never sees a half-written binary.
func copyExecutable(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tsp-*")
	if err != nil {
		return err
	}
	_, cerr := io.Copy(tmp, in)
	if err := firstErr(cerr, tmp.Chmod(0o755), tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func serviceUsage() {
	fmt.Print(`tsp service — run the local HTTPS proxy (https://*.localhost) from boot

Usage:
  tsp service install     install or update it (asks for your sudo password)
  tsp service uninstall   stop and remove it
  tsp service status      installed, running, and trusted? (exit 1 if not)

Run "tsp trust" once as well, so browsers trust its certificates.
`)
}

func cmdService(argv []string) int {
	if len(argv) == 0 || argv[0] == "-h" || argv[0] == "--help" {
		serviceUsage()
		if len(argv) == 0 {
			return 2
		}
		return 0
	}
	switch argv[0] {
	case "install", "uninstall", "status":
	default:
		serviceUsage()
		return 2
	}
	s, ok := platformService()
	if !ok && argv[0] != "status" {
		fmt.Fprintln(os.Stderr, "tsp service supports macOS (launchd) and Linux (systemd); elsewhere run `tsp proxy` yourself")
		return 1
	}
	switch argv[0] {
	case "install":
		return cmdServiceInstall(s, argv[1:])
	case "uninstall":
		if code, done := elevate(argv); done {
			return code
		}
		if err := s.uninstall(serviceRunner); err != nil {
			fmt.Fprintln(os.Stderr, "tsp service uninstall:", err)
			return 1
		}
		fmt.Println("✓ local HTTPS proxy removed (the CA stays in ~/.tailscale-proxy/ca; tsp trust added it to the OS trust store)")
		return 0
	default: // status
		return cmdServiceStatus(s, ok)
	}
}

func cmdServiceInstall(s serviceSystem, argv []string) int {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	uid := fs.Int("uid", -1, "user the proxy runs as (default: the sudo caller)")
	gid := fs.Int("gid", -1, "that user's group")
	home := fs.String("home", "", "that user's home")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if code, done := elevate(append([]string{"install"}, argv...)); done {
		return code
	}
	sp, err := serviceUser(*uid, *gid, *home, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp service install:", err)
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
	if err := s.install(serviceRunner, self, sp); err != nil {
		fmt.Fprintln(os.Stderr, "tsp service install:", err)
		return 1
	}
	fmt.Printf("✓ local HTTPS proxy installed: https://*.%s on 127.0.0.1:443 from boot (%s)\n", localTLD, s.unitPath)
	fmt.Println("  Next: tsp trust (once, so browsers trust it), then `tsp run` in an app's folder.")
	return 0
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

// elevate re-runs `tsp service <args>` through sudo for a non-root caller,
// passing who that caller is. done=false: already root, carry on.
func elevate(args []string) (code int, done bool) {
	if geteuid() == 0 {
		return 0, false
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "locating tsp:", err)
		return 1, true
	}
	home, _ := os.UserHomeDir()
	full := append([]string{self, "service"}, args...)
	if args[0] == "install" && !containsFlag(args, "--uid") {
		full = append(full, "--uid", strconv.Itoa(os.Getuid()), "--gid", strconv.Itoa(os.Getgid()), "--home", home)
	}
	fmt.Fprintln(os.Stderr, "tsp: this needs root (port 443 and the boot service): sudo", strings.Join(full[1:], " "))
	if err := interactive(append([]string{"sudo"}, full...)); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), true
		}
		fmt.Fprintln(os.Stderr, "sudo:", err)
		return 1, true
	}
	return 0, true
}

func containsFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
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
	line(running, fmt.Sprintf("proxy running (tsp %s, pid %d, port %d)", st.Version, st.PID, st.Port), "tsp service install (or `tsp proxy` in a terminal)")
	if running && st.Version != Version && Version != "dev" {
		fmt.Printf("  ! the proxy runs tsp %s, this is %s: tsp service install updates it\n", st.Version, Version)
	}
	ca, err := loadCA(caFiles(dir))
	line(err == nil && caTrusted(ca), "local CA trusted by the OS", "tsp trust (macOS: in a terminal at the Mac's screen)")
	if !ok {
		return 1
	}
	return 0
}
