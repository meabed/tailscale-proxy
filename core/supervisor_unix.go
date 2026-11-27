//go:build !windows

package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The boot service runs as root only to bind 443 (and 80, for the http redirect). It then drops to the user for
// good and stays as the supervisor: it holds the listening sockets for its whole
// life and runs the user's own tsp (~/.tsp/bin) as a worker that inherits them.
// Root never runs a file the user can write.
//
// A new release in bin/ (from a newer CLI, or the daily auto-update) is swapped in
// live: the supervisor starts it beside the serving worker on the same sockets; it
// must serve its own HTTPS health check through the local CA and say so over a
// pipe within readyTimeout. Only then is the old worker told to stop accepting and
// finish its requests. A release that fails to get ready, or exits within the
// probation, is marked bad (bin/state.json: never tried again) and the previous
// copy serves. A worker that crashes is restarted with backoff, and while no worker
// serves, the supervisor serves the names itself, so *.localhost never goes dark.

// The environment a supervisor hands its worker.
const (
	envListenFDs  = "TSP_LISTEN_FDS"  // comma-separated inherited listener fds (https)
	envHTTPFDs    = "TSP_HTTP_FDS"    // the same for port 80: http://*.localhost, redirected
	envListenPort = "TSP_LISTEN_PORT" // the port they listen on
	envLauncher   = "TSP_LAUNCHER"    // the supervisor's own release
	envReadyFD    = "TSP_READY_FD"    // the worker writes "ready" here once it serves
	envParentFD   = "TSP_PARENT_FD"   // EOF here: the supervisor is gone, so stop
)

// Supervision timings; shortened in tests.
var (
	readyTimeout    = 20 * time.Second
	probation       = 60 * time.Second
	binPoll         = 3 * time.Second
	livenessEvery   = 30 * time.Second
	crashBackoffMax = 30 * time.Second
)

// inherited is what a worker got from its supervisor.
type inherited struct {
	ok       bool
	lns      []net.Listener
	httpLns  []net.Listener
	port     int
	launcher string
	ready    *os.File // nil when not started by a supervisor
	parent   *os.File
}

// inheritedSockets returns the sockets a supervisor handed over, if any, and
// clears the variables so nothing this proxy starts inherits them.
func inheritedSockets() (inherited, error) {
	spec := os.Getenv(envListenFDs)
	if spec == "" {
		return inherited{}, nil
	}
	in := inherited{ok: true, launcher: os.Getenv(envLauncher)}
	in.port, _ = strconv.Atoi(os.Getenv(envListenPort))
	fdFile := func(key string) *os.File {
		if fd, err := strconv.Atoi(os.Getenv(key)); err == nil && fd >= 3 {
			return os.NewFile(uintptr(fd), key)
		}
		return nil
	}
	in.ready, in.parent = fdFile(envReadyFD), fdFile(envParentFD)
	httpSpec := os.Getenv(envHTTPFDs)
	for _, k := range []string{envListenFDs, envHTTPFDs, envListenPort, envLauncher, envReadyFD, envParentFD} {
		_ = os.Unsetenv(k)
	}
	var err error
	if in.lns, err = fileListeners(envListenFDs, spec); err != nil {
		return in, err
	}
	if httpSpec != "" {
		if in.httpLns, err = fileListeners(envHTTPFDs, httpSpec); err != nil {
			return in, err
		}
	}
	return in, nil
}

// fileListeners turns a comma-separated list of inherited fds into listeners.
func fileListeners(key, spec string) ([]net.Listener, error) {
	var lns []net.Listener
	for _, s := range strings.Split(spec, ",") {
		fd, err := strconv.Atoi(s)
		if err != nil || fd < 3 {
			return nil, fmt.Errorf("bad %s %q", key, spec)
		}
		f := os.NewFile(uintptr(fd), "tsp-listener-"+s)
		ln, err := net.FileListener(f) // dups the fd
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("inherited fd %d: %v", fd, err)
		}
		lns = append(lns, ln)
	}
	return lns, nil
}

// runWorker serves on the inherited sockets: it proves itself (selfCheck), says
// ready, then serves until SIGTERM (the supervisor swapping it out) or its
// supervisor is gone, finishing the requests in flight either way.
func runWorker(ctx context.Context, in inherited) int {
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	initAppLog(dir, "worker")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if in.parent != nil {
		go func() { _, _ = io.Copy(io.Discard, in.parent); cancel() }() // EOF: the supervisor exited
	}
	say := func(line string) {
		if in.ready != nil {
			_, _ = io.WriteString(in.ready, line+"\n")
			_ = in.ready.Close()
			in.ready = nil
		}
	}
	ls, err := newLocalServer(dir)
	if err == nil {
		err = ls.selfCheck(ctx)
	}
	if err != nil {
		appLog().Error("worker not ready", "err", err)
		say("fail " + err.Error())
		return 1
	}
	errc := ls.serve(in.lns)
	ls.serveRedirect(in.httpLns)
	say("ready")
	appLog().Info("worker serving", "port", in.port, "launcher", in.launcher)
	go adminServer(ctx, dir, func() *localServer { return ls })
	go historyLoop(ctx, dir, "local", ls.metrics)
	go autoUpdateLoop(ctx, dir)
	code := 0
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			appLog().Error("worker stopped", "err", err)
			code = 1
		}
	case <-ctx.Done():
	}
	ls.shutdown(drainTimeout)
	appLog().Info("worker drained")
	return code
}

// worker is one running copy of tsp under the supervisor.
type worker struct {
	version string
	cmd     *exec.Cmd
	done    chan struct{} // closed when it exited
	err     error
	ready   time.Time
	swapped bool     // it replaced a serving release: it is on probation
	parent  *os.File // held open: the worker stops at EOF
}

// startWorker runs release version from bin/ on the sockets and waits for it to
// report ready (or fail, or time out: then it is stopped and the error returned).
func startWorker(dir string, sk sockets, version string) (*worker, error) {
	bin := proxyBinPath(dir, version)
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer readyR.Close()
	parentR, parentW, err := os.Pipe()
	if err != nil {
		_ = readyW.Close()
		return nil, err
	}
	files, err := listenerFiles(sk.tls)
	var httpFiles []*os.File
	if err == nil {
		httpFiles, err = listenerFiles(sk.http)
	}
	if err != nil {
		for _, f := range append(files, readyW, parentR, parentW) {
			_ = f.Close()
		}
		return nil, err
	}
	fdList := func(first, n int) string {
		fds := make([]string, n)
		for i := range fds {
			fds[i] = strconv.Itoa(first + i)
		}
		return strings.Join(fds, ",")
	}
	cmd := exec.Command(bin, "proxy")
	cmd.ExtraFiles = append(append(files, httpFiles...), readyW, parentR)
	next := 3 + len(files) + len(httpFiles)
	cmd.Env = append(withoutEnv(os.Environ(), envListenFDs, envHTTPFDs, envListenPort, envLauncher, envReadyFD, envParentFD),
		envListenFDs+"="+fdList(3, len(files)), envListenPort+"="+strconv.Itoa(sk.port), envLauncher+"="+Version,
		envReadyFD+"="+strconv.Itoa(next), envParentFD+"="+strconv.Itoa(next+1))
	if len(httpFiles) > 0 {
		cmd.Env = append(cmd.Env, envHTTPFDs+"="+fdList(3+len(files), len(httpFiles)))
	}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.SysProcAttr = workerSysProcAttr()
	err = cmd.Start()
	for _, f := range cmd.ExtraFiles { // the worker has its own copies now
		_ = f.Close()
	}
	if err != nil {
		_ = parentW.Close()
		return nil, fmt.Errorf("starting %s: %v", bin, err)
	}
	w := &worker{version: normalizeVer(version), cmd: cmd, done: make(chan struct{}), parent: parentW}
	go func() { w.err = cmd.Wait(); _ = w.parent.Close(); close(w.done) }()

	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(readyR).ReadString('\n')
		line <- strings.TrimSpace(s)
	}()
	select {
	case got := <-line:
		if got == "ready" {
			w.ready = time.Now()
			return w, nil
		}
		w.stop(time.Second)
		if got == "" {
			<-w.done
			return nil, fmt.Errorf("tsp %s exited before it was ready: %v", version, w.err)
		}
		return nil, fmt.Errorf("tsp %s: %s", version, strings.TrimPrefix(got, "fail "))
	case <-time.After(readyTimeout):
		w.stop(time.Second)
		return nil, fmt.Errorf("tsp %s was not ready within %s", version, readyTimeout)
	}
}

// stop asks the worker to finish its requests and waits up to grace, then kills it.
func (w *worker) stop(grace time.Duration) {
	_ = w.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-w.done:
	case <-time.After(grace):
		_ = w.cmd.Process.Kill()
		<-w.done
	}
}

// listenerFiles dups each listener's socket for a child (the originals stay open
// here: the supervisor's own copies keep the port bound across swaps).
func listenerFiles(lns []net.Listener) ([]*os.File, error) {
	files := make([]*os.File, 0, len(lns))
	for _, ln := range lns {
		tl, ok := ln.(*net.TCPListener)
		if !ok {
			return nil, fmt.Errorf("listener %s is not TCP", ln.Addr())
		}
		f, err := tl.File()
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// dupListeners returns listeners on dups of lns' sockets, for the supervisor's own
// fallback server: closing them stops that server without unbinding the port.
func dupListeners(lns []net.Listener) ([]net.Listener, error) {
	files, err := listenerFiles(lns)
	if err != nil {
		return nil, err
	}
	out := make([]net.Listener, 0, len(files))
	for _, f := range files {
		ln, err := net.FileListener(f)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, ln)
	}
	return out, nil
}

// supervisor keeps one release serving on lns.
type supervisor struct {
	dir     string
	sk      sockets
	state   proxyState
	active  *worker
	builtin *localServer // the supervisor serving by itself (no worker)
	crashes []time.Time  // recent crashes of the current release
}

// runSupervisor is the boot service after the privilege drop.
func runSupervisor(ctx context.Context, sk sockets) int {
	dir, err := stateDir()
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	initAppLog(dir, "supervisor")
	// waitLock, not tryLock: a reader probing the lock (tsp doctor, tsp run) holds it for a moment.
	release, ok, err := waitLock(proxyLockPath(dir), 3*time.Second)
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "tsp proxy:", err)
		return 1
	case !ok:
		fmt.Fprintf(os.Stderr, "another tsp proxy already serves %s: tsp service status shows which\n", dir)
		return 1
	}
	defer release()
	s := &supervisor{dir: dir, sk: sk,
		state: proxyState{PID: os.Getpid(), Port: sk.port, Launcher: Version, Started: time.Now().UTC(), HTTPError: sk.httpErr}}
	if len(sk.http) > 0 {
		s.state.HTTPPort = localHTTPPort
	}
	defer os.Remove(proxyStatePath(dir))
	appLog().Info("supervisor started", "port", sk.port, "httpPort", s.state.HTTPPort)
	if sk.httpErr != "" {
		appLog().Warn("http redirect off: port 80 is taken", "err", sk.httpErr)
	}
	log.Printf("tsp %s supervises https://*.%s on port %d", Version, localTLD, sk.port)
	s.startBest()
	s.loop(ctx)
	if s.active != nil {
		s.active.stop(drainTimeout + 5*time.Second)
	}
	if s.builtin != nil {
		s.builtin.shutdown(drainTimeout)
	}
	appLog().Info("supervisor stopped")
	return 0
}

// candidate is the release in bin/ worth running: current, not bad, and not older
// than this launcher (an older copy would be a downgrade: the launcher serves).
func (s *supervisor) candidate() string {
	v := proxyBinVersion(s.dir)
	if v == "" || isBadVersion(s.dir, v) || (Version != "dev" && versionLess(v, Version)) {
		return ""
	}
	return v
}

// startBest runs the best release: the candidate, else (after a rollback) the
// previous one, else the supervisor's own built-in server.
func (s *supervisor) startBest() {
	for tries := 0; tries < 2; tries++ {
		v := s.candidate()
		if v == "" {
			break
		}
		w, err := startWorker(s.dir, s.sk, v)
		if err == nil {
			s.serveWith(w)
			return
		}
		s.rollback(v, err)
	}
	s.serveBuiltin("no runnable copy in " + binDir(s.dir))
}

// serveWith makes w the serving worker, stopping the built-in server if it ran.
func (s *supervisor) serveWith(w *worker) {
	s.active = w
	if s.builtin != nil {
		s.builtin.shutdown(drainTimeout)
		s.builtin = nil
	}
	s.state.Worker, s.state.Version, s.state.Mode = w.cmd.Process.Pid, w.version, "worker"
	s.saveState()
	appLog().Info("worker ready", "release", w.version, "worker", w.cmd.Process.Pid)
}

// serveBuiltin serves the names from the supervisor itself (no worker runs).
func (s *supervisor) serveBuiltin(why string) {
	if s.builtin != nil {
		return
	}
	lns, err := dupListeners(s.sk.tls)
	var httpLns []net.Listener
	if err == nil {
		httpLns, err = dupListeners(s.sk.http)
	}
	var ls *localServer
	if err == nil {
		ls, err = newLocalServer(s.dir)
	}
	if err != nil {
		appLog().Error("built-in server failed", "err", err)
		log.Printf("tsp: cannot serve: %v", err)
		return
	}
	ls.serve(lns)
	ls.serveRedirect(httpLns)
	s.builtin = ls
	s.state.Worker, s.state.Version, s.state.Mode = os.Getpid(), Version, "builtin"
	s.saveState()
	appLog().Warn("serving built in", "why", why)
}

func (s *supervisor) saveState() {
	if err := writeProxyState(s.dir, s.state); err != nil {
		appLog().Error("writing proxy.json", "err", err)
	}
}

// rollback sets release v aside for good and records why.
func (s *supervisor) rollback(v string, why error) {
	now := markBad(s.dir, v, why.Error())
	s.state.Rollbacks++
	s.saveState()
	appLog().Error("release rolled back", "release", v, "err", why, "current", now)
	log.Printf("tsp %s set aside: %v", v, why)
}

// loop watches bin/ for a newer release, the worker for an exit, and its health.
func (s *supervisor) loop(ctx context.Context) {
	poll := time.NewTicker(binPoll)
	defer poll.Stop()
	live := time.NewTicker(livenessEvery)
	defer live.Stop()
	misses := 0
	go adminServer(ctx, s.dir, func() *localServer { return s.builtin }) // the built-in server, when it serves

	for {
		var exited <-chan struct{}
		if s.active != nil {
			exited = s.active.done
		}
		select {
		case <-ctx.Done():
			return
		case <-exited:
			s.workerExited(ctx)
			misses = 0
		case <-poll.C:
			s.maybeSwap()
		case <-live.C:
			if s.active == nil {
				continue
			}
			if _, err := adminSocketPath(s.dir, s.active.cmd.Process.Pid); err != nil {
				continue // no admin socket on this machine: the exit watch still covers crashes
			}
			if _, err := adminClient(s.dir, s.active.cmd.Process.Pid, 5*time.Second).Get("http://tsp/health"); err != nil {
				misses++
				appLog().Warn("worker health check failed", "worker", s.active.cmd.Process.Pid, "misses", misses, "err", err)
				if misses >= 3 {
					_ = s.active.cmd.Process.Kill() // handled as a crash on the next turn
				}
			} else {
				misses = 0
			}
		}
	}
}

// maybeSwap moves to a newer release in bin/ once it proves it serves.
func (s *supervisor) maybeSwap() {
	v := s.candidate()
	if v == "" || (s.active != nil && !versionLess(s.active.version, v)) || (s.active == nil && s.builtin == nil) {
		return
	}
	from := s.state.Version
	w, err := startWorker(s.dir, s.sk, v)
	if err != nil {
		s.rollback(v, err)
		return
	}
	old := s.active
	w.swapped = true
	s.state.Swaps++ // saved with the new release by serveWith: proxy.json never shows one without the other
	s.crashes = nil
	s.serveWith(w)
	if old != nil {
		old.stop(drainTimeout + 5*time.Second)
	}
	appLog().Info("swapped", "from", from, "to", v)
	log.Printf("tsp %s now serves (was %s)", v, from)
}

// workerExited handles a worker that stopped on its own: within its probation a
// swapped-in release is rolled back; otherwise it is restarted, with backoff, and
// set aside after three crashes in five minutes. The built-in server covers the gap.
func (s *supervisor) workerExited(ctx context.Context) {
	w := s.active
	s.active = nil
	s.state.Crashes++
	appLog().Error("worker exited", "release", w.version, "err", w.err, "after", time.Since(w.ready).Round(time.Second).String())
	s.serveBuiltin("worker exited")
	now := time.Now()
	recent := []time.Time{now}
	for _, t := range s.crashes {
		if now.Sub(t) < 5*time.Minute {
			recent = append(recent, t)
		}
	}
	s.crashes = recent
	switch {
	case w.swapped && now.Sub(w.ready) < probation:
		s.rollback(w.version, fmt.Errorf("exited %s after it took over: %v", now.Sub(w.ready).Round(time.Second), w.err))
		s.crashes = nil
	case len(recent) >= 3:
		s.rollback(w.version, fmt.Errorf("crashed %d times in 5 minutes: %v", len(recent), w.err))
		s.crashes = nil
	default:
		wait := min(time.Second<<(len(recent)-1), crashBackoffMax)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
	v := s.candidate()
	if v == "" {
		return
	}
	if nw, err := startWorker(s.dir, s.sk, v); err == nil {
		s.serveWith(nw)
	} else {
		s.rollback(v, err)
	}
}

func withoutEnv(env []string, keys ...string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		keep := true
		for _, drop := range keys {
			if k == drop {
				keep = false
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}
