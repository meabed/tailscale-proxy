package core

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// `tsp run` starts an app on a free port and serves it at https://<name>.localhost
// while it runs:
//
//	tsp run                          # package.json "tsp": {"name": "api.acme", "script": "dev:app"}
//	tsp run api.acme -- bun src/index.ts
//	TSP=0 tsp run                    # run the script as is: no port, no name
//
// The app gets PORT, HOST=127.0.0.1 and (unless set) NODE_EXTRA_CA_CERTS.

func runUsage() {
	fmt.Print(`tsp run — run an app at https://<name>.localhost

Usage:
  tsp run [--force]                     name and script from package.json "tsp"
  tsp run [--force] <name> -- <cmd...>  any command

  package.json:  "scripts": { "dev": "tsp run", "dev:app": "bun --hot src/index.ts" },
                 "tsp": { "name": "api.acme", "script": "dev:app" }

The app gets PORT (stable per name, 4000-4999), HOST=127.0.0.1 and
NODE_EXTRA_CA_CERTS (the local CA). TSP=0 runs the script unchanged (its own port).

Flags:
  --force   take the name over from another running "tsp run"
`)
}

// runSpec is what to start, resolved from the arguments or package.json.
type runSpec struct {
	name string
	argv []string
}

// packageJSON is the part of package.json `tsp run` reads.
type packageJSON struct {
	TSP *struct {
		Name   string `json:"name"`
		Script string `json:"script"`
	} `json:"tsp"`
	Scripts map[string]string `json:"scripts"`
}

// resolveRun turns the arguments (or ./package.json) into a name and command.
// The script runs with the package manager that invoked `tsp run` (npm_execpath).
func resolveRun(args []string, dir string, getenv func(string) string) (runSpec, error) {
	if len(args) > 0 {
		if len(args) > 1 && args[1] == "--" {
			args = append(args[:1], args[2:]...)
		}
		if len(args) < 2 {
			return runSpec{}, errors.New("give a command after the name: tsp run <name> -- <cmd...>")
		}
		return runSpec{name: args[0], argv: args[1:]}, validateName(args[0])
	}
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return runSpec{}, fmt.Errorf("no package.json here (%v): run `tsp run <name> -- <cmd...>`, or add \"tsp\": {\"name\", \"script\"} to package.json", err)
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return runSpec{}, fmt.Errorf("reading package.json: %v", err)
	}
	if pkg.TSP == nil || pkg.TSP.Name == "" || pkg.TSP.Script == "" {
		return runSpec{}, errors.New(`package.json needs "tsp": {"name": "<name>", "script": "<script to run>"}`)
	}
	if _, ok := pkg.Scripts[pkg.TSP.Script]; !ok {
		return runSpec{}, fmt.Errorf("package.json has no script %q (named in \"tsp\".script)", pkg.TSP.Script)
	}
	pm := getenv("npm_execpath")
	if pm == "" {
		pm = "npm"
	}
	return runSpec{name: pkg.TSP.Name, argv: []string{pm, "run", pkg.TSP.Script}}, validateName(pkg.TSP.Name)
}

// runEnv is the app's environment: the caller's plus the port, the loopback
// host, and the local CA for Node/Bun clients calling other *.localhost names.
func runEnv(base []string, port int, caPath string, getenv func(string) string) []string {
	env := append(append([]string(nil), base...), "PORT="+strconv.Itoa(port), "HOST=127.0.0.1")
	if caPath != "" && getenv("NODE_EXTRA_CA_CERTS") == "" {
		env = append(env, "NODE_EXTRA_CA_CERTS="+caPath)
	}
	return env
}

// splitRunArgs separates `[flags] [<name>] [-- <cmd...>]`: everything after the
// first "--" is the command, untouched; flags must come before the name.
func splitRunArgs(argv []string) (flags, rest []string) {
	for i, a := range argv {
		if a == "--" {
			return argv[:i], append([]string{"--"}, argv[i+1:]...)
		}
	}
	return argv, nil
}

func cmdRun(argv []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = runUsage
	force := fs.Bool("force", false, "take the name over")
	flags, cmd := splitRunArgs(argv)
	if err := fs.Parse(flags); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	args := fs.Args()
	if len(args) > 1 || (len(args) == 1 && cmd == nil) {
		// `tsp run api bun x` / `tsp run api --force -- x`: anything after the name needs the "--".
		fmt.Fprintf(os.Stderr, "tsp run: put flags before the name and the command after --: tsp run [--force] <name> -- <cmd...> (got %q)\n", strings.Join(argv, " "))
		return 2
	}
	if len(cmd) > 0 && len(args) == 0 {
		fmt.Fprintln(os.Stderr, "tsp run: name the app before --: tsp run <name> -- <cmd...>")
		return 2
	}
	cwd, _ := os.Getwd()
	spec, err := resolveRun(append(args, cmd...), cwd, os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp run:", err)
		return 2
	}
	if os.Getenv("TSP") == "0" {
		return runChild(spec.argv, os.Environ())
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp run:", err)
		return 1
	}
	reg := newLocalRegistry(filepath.Join(dir, "routes"))
	release, ok, err := reg.claim(spec.name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp run:", err)
		return 1
	}
	if !ok {
		prev, _ := reg.read(spec.name)
		if !*force {
			fmt.Fprintf(os.Stderr, "tsp run: %s already runs (pid %d, %s, port %d). Stop it, or take it over with: tsp run --force\n", localRoute{Name: spec.name}.URL(), prev.PID, prev.Dir, prev.Port)
			return 1
		}
		if release, ok = takeOver(reg, spec.name, prev); !ok {
			fmt.Fprintf(os.Stderr, "tsp run: pid %d did not give %s up within 5s\n", prev.PID, spec.name)
			return 1
		}
	}
	defer release()
	defer reg.remove(spec.name) // before the claim is released (defers run last-in first-out)

	taken := map[int]bool{}
	for _, rt := range reg.list() {
		taken[rt.Port] = true
	}
	port, err := pickPort(spec.name, taken, portFree)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp run:", err)
		return 1
	}
	rt := localRoute{Name: spec.name, Port: port, PID: os.Getpid(), Dir: cwd}
	if err := reg.add(rt); err != nil {
		fmt.Fprintln(os.Stderr, "tsp run:", err)
		return 1
	}
	caPath := ""
	if ca, err := ensureCA(dir); err == nil {
		caPath = ca.certPEM
	}
	fmt.Fprintf(os.Stderr, "tsp: %s → 127.0.0.1:%d\n", rt.URL(), port)
	healProxy(dir, rt)
	return runChild(spec.argv, runEnv(os.Environ(), port, caPath, os.Getenv))
}

// healProxy makes sure the local HTTPS proxy runs this tsp version. In a
// terminal it sets it up (tsp service install: sudo only when the boot service
// must change); elsewhere (CI, a pipe) it only says what to run.
func healProxy(dir string, rt localRoute) {
	st, ok := runningProxy(dir)
	if ok && !st.outdated() {
		return
	}
	s, supported := servicePlatform()
	if supported && stdinIsTerminal() {
		self, err := os.Executable()
		if err == nil {
			self, err = filepath.EvalSymlinks(self)
		}
		if err == nil && setupLocalHTTPS(s, self) == 0 {
			return
		}
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "tsp: ⚠ the local HTTPS proxy is not running, so %s will not answer: tsp service install\n", rt.URL())
		return
	}
	fmt.Fprintf(os.Stderr, "tsp: the local proxy runs tsp %s (this is %s): tsp service install updates it\n", st.Version, Version)
}

// stdinIsTerminal is replaced in tests.
var stdinIsTerminal = func() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// takeOver asks the run that holds name (prev) to stop, then claims the name as
// soon as its lock is free (up to 5s). The pid comes from the holder's own route
// file and the holder still has the lock, so the signal reaches that run.
func takeOver(reg *localRegistry, name string, prev localRoute) (release func(), ok bool) {
	fmt.Fprintf(os.Stderr, "tsp: taking %s over from pid %d\n", localRoute{Name: name}.URL(), prev.PID)
	if prev.PID > 0 {
		_ = stopProcess(prev.PID)
	}
	release, ok, err := waitLock(reg.lock(name), 5*time.Second)
	return release, err == nil && ok
}

// interruptGrace is how long tsp waits after a SIGINT before passing it on.
const interruptGrace = 2 * time.Second

// runChild runs argv with env and stdio attached and returns its exit code.
// SIGTERM and SIGHUP are passed on. Ctrl-C in the terminal already reaches the
// app (same process group), so a SIGINT is passed on only if the app is still
// running after interruptGrace (it was sent to tsp alone, e.g. kill -INT).
func runChild(argv []string, env []string) int {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "tsp run: starting %s: %v\n", argv[0], err)
		return 127
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	pass := func(sig os.Signal) {
		if err := cmd.Process.Signal(sig); err != nil {
			_ = cmd.Process.Kill()
		}
	}
	var late <-chan time.Time
	for {
		select {
		case sig := <-sigs:
			if sig == os.Interrupt {
				late = time.After(interruptGrace)
				continue
			}
			pass(sig)
		case <-late:
			pass(os.Interrupt)
		case err := <-done:
			var exit *exec.ExitError
			switch {
			case err == nil:
				return 0
			case errors.As(err, &exit) && exit.ExitCode() >= 0:
				return exit.ExitCode()
			default:
				return 1 // ended by a signal, or Wait failed
			}
		}
	}
}
