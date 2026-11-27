package core

// `tsp stop` and the dashboard's Stop button: end the app behind a name, or what
// listens on a port, the way Ctrl-C would (SIGTERM; taskkill on Windows), and at
// once with --force (SIGKILL; taskkill /F). It asks first in a terminal.
//
//   tsp stop web.acme              the app at https://web.acme.localhost
//   tsp stop 3000                  whatever listens on port 3000
//   tsp stop --pid 54607           one process
//   tsp stop web.acme --force      no grace period
//   tsp stop web.acme --yes        don't ask (scripts, agents)

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// stopGrace is how long a stop waits for the app to exit before it says so.
var stopGrace = 5 * time.Second

// stopTarget is what a stop ends: the process behind a name or a port.
type stopTarget struct {
	Name      string     `json:"name,omitempty"` // the route it serves, when it is one
	Source    string     `json:"source"`         // run, found, port or pid
	Port      int        `json:"port,omitempty"`
	PID       int        `json:"pid"`                 // the one a graceful stop signals (tsp run's, for run)
	Tree      []int      `json:"tree,omitempty"`      // every process PID started; --force ends them too
	Listeners []int      `json:"listeners,omitempty"` // the processes on Port; --force ends them too
	Command   string     `json:"command,omitempty"`   // PID's command line, shortened
	Dir       string     `json:"dir,omitempty"`
	Process   *procStats `json:"process,omitempty"` // CPU and memory of PID and its tree (not on Windows)
}

// inspect fills in the command line, the processes it started and what they
// cost, from one ps.
func (t *stopTarget) inspect(s *cpuSampler) {
	t.Command = processCommands([]int{t.PID})[t.PID]
	table := psTable()
	t.Tree = descendants(table, t.PID)
	if st, ok := s.stats(table, []int{t.PID})[t.PID]; ok {
		t.Process = &st
	}
}

// pids is every process a forced stop ends: the target, the processes it
// started, and its listeners.
func (t stopTarget) pids() []int {
	out := []int{t.PID}
	for _, p := range slices.Concat(t.Tree, t.Listeners) {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// what names the target for people: its URL, else its port, else its pid.
func (t stopTarget) what() string {
	switch {
	case t.Name != "":
		return localRoute{Name: t.Name}.URL()
	case t.Port > 0:
		return "port " + strconv.Itoa(t.Port)
	default:
		return "pid " + strconv.Itoa(t.PID)
	}
}

// tspArg is the `tsp stop` argument that reaches this target again.
func (t stopTarget) tspArg() string {
	switch {
	case t.Name != "":
		return t.Name
	case t.Port > 0:
		return strconv.Itoa(t.Port)
	default:
		return "--pid " + strconv.Itoa(t.PID)
	}
}

// stopCommand is one way to stop a target from a terminal, for this OS.
type stopCommand struct {
	Label   string `json:"label"`
	Command string `json:"command"`
}

// stopCommands are the commands that stop t by hand, on this OS: tsp's own, the
// OS's (graceful, forced) and, with a port, by port.
func stopCommands(t stopTarget) []stopCommand {
	cmds := []stopCommand{{"tsp", "tsp stop " + t.tspArg()}}
	pid := strconv.Itoa(t.PID)
	if runtime.GOOS == "windows" {
		cmds = append(cmds, stopCommand{"Stop", "taskkill /PID " + pid + " /T"}, stopCommand{"Force", "taskkill /PID " + pid + " /T /F"})
		if t.Port > 0 {
			cmds = append(cmds, stopCommand{"By port (PowerShell)", fmt.Sprintf("Get-NetTCPConnection -LocalPort %d -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess }", t.Port)})
		}
		return cmds
	}
	cmds = append(cmds, stopCommand{"Stop (SIGTERM)", "kill " + pid}, stopCommand{"Force (SIGKILL)", "kill -9 " + joinPIDs(t.pids())})
	if t.Port > 0 {
		cmds = append(cmds, stopCommand{"By port", fmt.Sprintf("lsof -ti tcp:%d -sTCP:LISTEN | xargs kill", t.Port)})
	}
	return cmds
}

// signalCommand is what a stop runs, as the command that does the same by hand.
func signalCommand(t stopTarget, force bool) string {
	if runtime.GOOS == "windows" {
		cmd := "taskkill /PID " + strconv.Itoa(t.PID) + " /T"
		if force {
			cmd += " /F"
		}
		return cmd
	}
	if force {
		return "kill -KILL " + joinPIDs(t.pids())
	}
	return "kill -TERM " + strconv.Itoa(t.PID)
}

func joinPIDs(pids []int, sep ...string) string {
	s := make([]string, len(pids))
	for i, p := range pids {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, append(sep, " ")[0])
}

// stopResult is how a stop went, for the CLI and /api/apps/{name}/stop.
type stopResult struct {
	Stopped bool   `json:"stopped"`
	Ran     string `json:"ran"`            // the equivalent command
	Took    string `json:"took,omitempty"` // until every process was gone
	Error   string `json:"error,omitempty"`
	Next    string `json:"next,omitempty"` // what to do when it did not stop
}

// guardTarget refuses processes a stop must never end: init, this tsp, the
// proxy that serves the names, and the process that started this one.
func guardTarget(dir string, t stopTarget) error {
	st, _ := readProxyState(dir)
	for _, pid := range t.pids() {
		switch {
		case pid <= 1:
			return fmt.Errorf("pid %d is not a process tsp stops", pid)
		case pid == os.Getpid(), pid == os.Getppid():
			return fmt.Errorf("pid %d is this tsp, or the program that started it", pid)
		case pid == st.PID, pid == st.Worker:
			return fmt.Errorf("pid %d is tsp's own local HTTPS proxy: tsp service uninstall stops it", pid)
		}
	}
	return nil
}

// stopProcesses ends t: a graceful stop signals t.PID (tsp run passes it on to
// its app), a forced one ends every process of t at once; then it waits for them
// to exit, up to stopGrace.
func stopProcesses(t stopTarget, force bool) stopResult {
	res := stopResult{Ran: signalCommand(t, force)}
	start := time.Now()
	targets := []int{t.PID}
	if force {
		targets = t.pids()
	}
	for _, pid := range targets {
		if err := signalStop(pid, force); err != nil {
			res.Error = err.Error()
			return res
		}
	}
	deadline := start.Add(stopGrace)
	for {
		alive := slices.ContainsFunc(t.pids(), pidRunning)
		if !alive {
			res.Stopped, res.Took = true, time.Since(start).Round(10*time.Millisecond).String()
			return res
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	res.Error = fmt.Sprintf("still running %s after %s", t.what(), stopGrace)
	if !force {
		res.Next = "tsp stop " + t.tspArg() + " --force   (" + signalCommand(t, true) + ")"
	}
	return res
}

// listenersOn are the processes listening on port right now, and whether the
// port is a Docker container's (its forwarder is Docker itself, never stopped).
func listenersOn(port int) (pids []int, docker bool) {
	ls, _ := newDiscoverer(execRunner{}).listeners(portRange{port, port})
	for _, l := range ls {
		if l.Port != port || l.PID <= 0 {
			continue
		}
		docker = docker || runtimeOf(l) == "docker"
		if !slices.Contains(pids, l.PID) {
			pids = append(pids, l.PID)
		}
	}
	sort.Ints(pids)
	return pids, docker
}

// dockerPort says how to stop a container instead of Docker.
func dockerPort(port int) error {
	return fmt.Errorf("port %d is a Docker container's, and stopping its process would stop Docker itself: docker ps --filter publish=%d shows the container, docker stop <id> stops it", port, port)
}

// targetOf is the stop target behind a served route, checked against the
// processes running now (a pid that exited, or was reused, is not signalled).
func targetOf(reg *localRegistry, rt localRoute, s *cpuSampler) (stopTarget, error) {
	t := stopTarget{Name: rt.Name, Source: rt.Source, Port: rt.Port, PID: rt.PID, Dir: rt.Dir}
	pids, docker := listenersOn(rt.Port)
	t.Listeners = pids
	switch {
	case docker || rt.Runtime == "docker":
		return t, dockerPort(rt.Port)
	case rt.Source == sourceRun:
		if cur, ok := reg.read(rt.Name); !ok || cur.PID != rt.PID || !reg.alive(rt.Name) {
			return t, fmt.Errorf("%s has stopped already", rt.URL())
		}
	default:
		if !slices.Contains(t.Listeners, rt.PID) {
			return t, fmt.Errorf("%s has stopped already (nothing listens on port %d as pid %d)", rt.URL(), rt.Port, rt.PID)
		}
	}
	t.inspect(s)
	return t, nil
}

// currentRoutes is every name served now: tsp run's from the registry, the found
// ones from the running proxy, and notes on what could not be read.
func currentRoutes(dir string) (reg *localRegistry, routes []localRoute, notes []string) {
	reg = newLocalRegistry(filepath.Join(dir, "routes"))
	routes = reg.list()
	held := map[string]bool{}
	for i := range routes {
		routes[i].Source = sourceRun
		held[routes[i].Name] = true
	}
	if st, ok := runningProxy(dir); !ok {
		notes = append(notes, "the local HTTPS proxy is not running, so nothing is found or served: tsp service install")
	} else if ws, err := fetchWatchStatus(dir, st.Worker); err != nil {
		notes = append(notes, "found names unknown: the proxy (tsp "+st.Version+") did not answer: tsp doctor")
	} else {
		if !ws.Enabled {
			notes = append(notes, "finding dev servers is off: only tsp run apps are served (tsp configure --discover turns it on)")
		}
		for _, rt := range ws.Routes {
			if !held[rt.Name] {
				routes = append(routes, rt)
			}
		}
		for _, p := range ws.Problems {
			notes = append(notes, "skipped: "+p)
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Name < routes[j].Name })
	return reg, routes, notes
}

func stopUsage() {
	fmt.Print(`tsp stop — end the app behind a name, or what listens on a port

Usage:
  tsp stop <name> | <port> | --pid <pid> [--force] [--yes]

It sends SIGTERM (Windows: taskkill /T), like Ctrl-C in the app's terminal, and
waits up to 5s for it to exit. A tsp run app ends with its tsp run.

Flags:
  --pid <n>   stop this process
  --force     end it and every process it started at once, for an app that
              hangs (SIGKILL; Windows: taskkill /T /F): no chance to clean up
  --yes       don't ask first (needed without a terminal)

Examples:
  tsp stop web.acme          # https://web.acme.localhost
  tsp stop 3000              # whatever listens on port 3000
  tsp stop web.acme --force  # it ignored the graceful stop
  tsp stop --pid 54607 --yes
`)
}

func cmdStop(argv []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.Usage = stopUsage
	pid := fs.Int("pid", 0, "stop this process")
	force := fs.Bool("force", false, "end it at once")
	yes := fs.Bool("yes", false, "don't ask first")
	// Flags may follow the name: tsp stop web --force.
	var args []string
	for len(argv) > 0 {
		if code, stop := parseFlags(fs, argv); stop {
			return code
		}
		if fs.NArg() == 0 {
			break
		}
		args, argv = append(args, fs.Arg(0)), fs.Args()[1:]
	}
	if (*pid > 0) == (len(args) == 1) || len(args) > 1 {
		stopUsage()
		return 2
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	t, err := resolveStop(dir, args, *pid)
	if err == nil {
		err = guardTarget(dir, t)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp stop:", err)
		return 1
	}
	printStopTarget(t)
	if !*yes {
		if !stdinIsTerminal() {
			fmt.Fprintf(os.Stderr, "tsp stop: add --yes to stop it without asking (no terminal to ask in): tsp stop %s --yes\n", t.tspArg())
			return 2
		}
		fmt.Printf("Stop it (%s)? [y/N] ", signalCommand(t, *force))
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Println("Left running.")
			return 1
		}
	}
	res := stopProcesses(t, *force)
	if !res.Stopped {
		fmt.Printf("✗ %s: %s\n", t.what(), res.Error)
		if res.Next != "" {
			fmt.Println("  It may be finishing work, or it ignores SIGTERM. End it at once: " + res.Next)
		}
		return 1
	}
	fmt.Printf("✓ stopped %s in %s (%s)\n", t.what(), res.Took, res.Ran)
	if t.Source == sourceRun {
		fmt.Println("  Its tsp run ended with it: start it again with the same command.")
	}
	return 0
}

// resolveStop finds what `tsp stop <arg>` or `tsp stop --pid <n>` means now.
func resolveStop(dir string, args []string, pid int) (stopTarget, error) {
	reg, routes, notes := currentRoutes(dir)
	if pid > 0 {
		if !pidRunning(pid) {
			return stopTarget{}, fmt.Errorf("no process %d is running", pid)
		}
		t := stopTarget{Source: "pid", PID: pid}
		for _, rt := range routes {
			if rt.PID == pid {
				t.Name, t.Port, t.Dir, t.Source = rt.Name, rt.Port, rt.Dir, rt.Source
			}
		}
		t.inspect(&cpuSampler{})
		return t, nil
	}
	arg := args[0]
	if port, err := strconv.Atoi(arg); err == nil {
		if port < 1 || port > 65535 {
			return stopTarget{}, fmt.Errorf("%d is not a port (1-65535); a process id goes with --pid", port)
		}
		for _, rt := range routes {
			if rt.Port == port && rt.Source == sourceRun {
				return targetOf(reg, rt, &cpuSampler{})
			}
		}
		pids, docker := listenersOn(port)
		switch {
		case docker:
			return stopTarget{}, dockerPort(port)
		case len(pids) == 0:
			return stopTarget{}, fmt.Errorf("nothing listens on port %d", port)
		}
		t := stopTarget{Source: "port", Port: port, PID: pids[0], Listeners: pids}
		for _, rt := range routes {
			if rt.Port == port && slices.Contains(pids, rt.PID) {
				t.Name, t.Dir, t.PID = rt.Name, rt.Dir, rt.PID
			}
		}
		t.inspect(&cpuSampler{})
		return t, nil
	}
	name := nameFromHost(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(arg, "https://"), "http://"), "/"))
	if name == "" {
		name = arg
	}
	for _, rt := range routes {
		if rt.Name == name {
			return targetOf(reg, rt, &cpuSampler{})
		}
	}
	msg := fmt.Sprintf("nothing runs at %s", localRoute{Name: name}.URL())
	if len(routes) > 0 {
		names := make([]string, len(routes))
		for i, rt := range routes {
			names[i] = rt.Name
		}
		msg += "; running: " + strings.Join(names, ", ")
	}
	for _, n := range notes {
		msg += "\n  note: " + n
	}
	return stopTarget{}, errors.New(msg)
}

func printStopTarget(t stopTarget) {
	fmt.Printf("%s\n", t.what())
	if t.Command != "" {
		fmt.Printf("  process  %s (pid %d)\n", t.Command, t.PID)
	} else {
		fmt.Printf("  process  pid %d\n", t.PID)
	}
	if t.Source == sourceRun {
		fmt.Println("  started  with tsp run: it passes the stop on to its app")
	}
	if p := t.Process; p != nil {
		fmt.Printf("  using    %s; running %s\n", p.summary(), shortDuration(time.Duration(p.UpSeconds*float64(time.Second))))
	}
	if t.Port > 0 {
		fmt.Printf("  port     %d\n", t.Port)
	}
	if t.Dir != "" {
		fmt.Printf("  folder   %s\n", homeShort(t.Dir))
	}
}

// shortCommand keeps a command line readable: its first 160 characters.
func shortCommand(cmd string) string {
	cmd = strings.Join(strings.Fields(cmd), " ")
	if len(cmd) > 160 {
		cmd = cmd[:157] + "..."
	}
	return safeText(cmd)
}
