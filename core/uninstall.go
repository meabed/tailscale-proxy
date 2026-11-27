package core

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// `tsp uninstall` takes back everything tsp set up on this machine, safe to run
// again: the Serve/Funnel entry pointing at tsp, the boot service, the local CA's
// trust, and ~/.tailscale-proxy. The CLI itself is removed the way it was
// installed (brew, npm), which it prints; a standalone binary goes with --all.

func uninstallUsage() {
	fmt.Print(`tsp uninstall — remove everything tsp set up on this machine

Usage:
  tsp uninstall [--yes] [--all]

Removes, each only if present:
  the Tailscale Serve/Funnel entry that points at tsp
  the boot service (the local HTTPS proxy; sudo asks for your password)
  the local CA from the OS trust store (macOS: a dialog at the Mac's screen)
  ~/.tailscale-proxy: the CA, running names, the proxy's copy of tsp
Then prints how to remove the tsp command itself (brew uninstall, npm rm).

Flags:
  --yes   don't ask first (needed without a terminal)
  --all   also config.json, and a standalone tsp binary itself

Examples:
  tsp uninstall           # asks, then removes; run it again any time, it is safe
  tsp uninstall --yes --all
`)
}

// uninstallStep prints one line: ✓ done, – nothing to do, ✗ failed (with the next step).
func uninstallStep(ok bool, skipped bool, line, next string) bool {
	switch {
	case skipped:
		fmt.Println("– " + line)
	case ok:
		fmt.Println("✓ " + line)
	default:
		fmt.Println("✗ " + line)
		if next != "" {
			fmt.Println("    → " + next)
		}
	}
	return ok || skipped
}

func cmdUninstall(argv []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.Usage = uninstallUsage
	yes := fs.Bool("yes", false, "don't ask first")
	all := fs.Bool("all", false, "also config.json and a standalone binary")
	if code, stop := parseFlags(fs, argv); stop {
		return code
	}
	if fs.NArg() > 0 {
		uninstallUsage()
		return 2
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !*yes {
		if !stdinIsTerminal() {
			fmt.Fprintln(os.Stderr, "tsp uninstall: add --yes to remove without asking (no terminal to ask in)")
			return 2
		}
		fmt.Print("Remove tsp's boot service, its CA from the trust store, and ~/.tailscale-proxy? [y/N] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Println("Nothing removed.")
			return 1
		}
	}
	ok := true
	ok = uninstallExposure() && ok
	ok = uninstallService() && ok
	ok = uninstallTrust(dir) && ok
	ok = uninstallState(dir, *all) && ok
	uninstallBinary(*all)
	if !ok {
		fmt.Println("\nSome steps failed: fix the ✗ lines, then run tsp uninstall again (it skips what is gone).")
		return 1
	}
	return 0
}

// uninstallExposure removes the Serve/Funnel entry when it points at tsp's local
// proxy port; anyone else's entry stays.
func uninstallExposure() bool {
	if _, err := lookPath("tailscale"); err != nil {
		return uninstallStep(true, true, "Tailscale Serve/Funnel: tailscale is not installed", "")
	}
	cfg, _, _, _ := LoadConfig()
	target := "127.0.0.1:" + strconv.Itoa(cfg.Port)
	out, _, err := serviceRunner.Run("tailscale", "serve", "status")
	if err != nil || !strings.Contains(out, target) {
		return uninstallStep(true, true, "Tailscale Serve/Funnel: no entry points at tsp", "")
	}
	for _, m := range []Mode{ModeFunnel, ModeServe} {
		if err := exposeReset(serviceRunner, m); err != nil {
			return uninstallStep(false, false, "Tailscale Serve/Funnel entry: "+err.Error(), "tsp reset")
		}
	}
	return uninstallStep(true, false, "Tailscale Serve/Funnel entry removed (it pointed at "+target+")", "")
}

// uninstallService removes the boot service through `tsp service uninstall` (sudo).
func uninstallService() bool {
	s, supported := servicePlatform()
	if !supported || !s.installed() {
		return uninstallStep(true, true, "boot service: not installed", "")
	}
	code, done := elevate([]string{"uninstall"}, "removing the boot service")
	if !done {
		if err := s.uninstall(serviceRunner); err != nil {
			return uninstallStep(false, false, "boot service: "+err.Error(), "sudo tsp service uninstall")
		}
		code = 0
	}
	return uninstallStep(code == 0, false, "boot service removed: nothing answers https://*.localhost now", "sudo tsp service uninstall")
}

// untrustCommands returns the commands that take the CA out of the OS trust store
// on goos, by its name (it may be from an earlier CA that is gone from disk).
func untrustCommands(goos, caPath, home string, root bool) [][]string {
	switch goos {
	case "darwin":
		keychain := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
		var cmds [][]string
		if caPath != "" {
			cmds = append(cmds, []string{"security", "remove-trusted-cert", caPath})
		}
		return append(cmds, []string{"security", "delete-certificate", "-c", "tailscale-proxy local CA", keychain})
	case "windows":
		return [][]string{{"certutil", "-user", "-delstore", "Root", "tailscale-proxy local CA"}}
	}
	cmds := [][]string{
		{"rm", "-f", "/usr/local/share/ca-certificates/tailscale-proxy-ca.crt", "/etc/pki/ca-trust/source/anchors/tailscale-proxy-ca.pem"},
	}
	if _, err := lookPath("update-ca-certificates"); err == nil {
		cmds = append(cmds, []string{"update-ca-certificates", "--fresh"})
	} else if _, err := lookPath("update-ca-trust"); err == nil {
		cmds = append(cmds, []string{"update-ca-trust"})
	}
	if !root {
		for i, c := range cmds {
			cmds[i] = append([]string{"sudo"}, c...)
		}
	}
	return cmds
}

// uninstallTrust takes the CA out of the trust store when it is trusted.
func uninstallTrust(dir string) bool {
	ca, err := loadCA(caFiles(dir))
	if err != nil || !caTrusted(ca) {
		return uninstallStep(true, true, "local CA: not in the OS trust store", "")
	}
	if runtime.GOOS == "darwin" && os.Getenv("SSH_CONNECTION") != "" {
		return uninstallStep(false, false, "local CA still trusted: macOS asks for that in a dialog at the Mac's screen",
			"run `tsp uninstall` in a terminal there, or remove \"tailscale-proxy local CA\" in Keychain Access")
	}
	home, _ := os.UserHomeDir()
	for _, c := range untrustCommands(runtime.GOOS, ca.certPEM, home, geteuid() == 0) {
		if err := interactive(c); err != nil && c[len(c)-1] != "update-ca-trust" {
			return uninstallStep(false, false, "local CA: "+strings.Join(c, " ")+": "+err.Error(),
				`remove "tailscale-proxy local CA" from the trust store by hand (Keychain Access on macOS)`)
		}
	}
	return uninstallStep(true, false, "local CA removed from the OS trust store", "")
}

// uninstallState deletes ~/.tailscale-proxy, keeping config.json unless all.
func uninstallState(dir string, all bool) bool {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return uninstallStep(true, true, dir+": already gone", "")
	}
	if err != nil {
		return uninstallStep(false, false, dir+": "+err.Error(), "rm -rf "+dir)
	}
	kept := false
	for _, e := range entries {
		if e.Name() == "config.json" && !all {
			kept = true
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return uninstallStep(false, false, dir+": "+err.Error(), "rm -rf "+dir)
		}
	}
	if kept {
		return uninstallStep(true, false, dir+" emptied (config.json kept: --all removes it)", "")
	}
	_ = os.Remove(dir)
	return uninstallStep(true, false, dir+" removed", "")
}

// uninstallBinary removes a standalone tsp with --all, and otherwise says how the
// tsp command goes, the way it was installed.
func uninstallBinary(all bool) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	switch installMethod(exe) {
	case "brew":
		fmt.Println("\nLast step, the tsp command: brew uninstall tsp")
	case "npm":
		fmt.Println("\nLast step, the tsp command: npm rm -g tailscale-proxy")
	case "npx":
		fmt.Println("\nNothing else: npx keeps no tsp installed.")
	default:
		if !all {
			fmt.Println("\nLast step, the tsp command: rm " + exe + "   (or tsp uninstall --all)")
			return
		}
		if err := os.Remove(exe); err != nil {
			uninstallStep(false, false, "tsp binary "+exe+": "+err.Error(), "rm "+exe)
			return
		}
		uninstallStep(true, false, "tsp binary "+exe+" removed", "")
	}
}
