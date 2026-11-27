//go:build !windows

package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

var syscallZero = syscall.Signal(0)

// fastSupervision shortens the supervisor's timings for a test.
func fastSupervision(t *testing.T) {
	t.Helper()
	saved := []time.Duration{readyTimeout, probation, binPoll, livenessEvery, crashBackoffMax, drainTimeout}
	readyTimeout, probation, binPoll, livenessEvery, crashBackoffMax, drainTimeout =
		5*time.Second, 2*time.Second, 30*time.Millisecond, time.Hour, 200*time.Millisecond, 2*time.Second
	t.Cleanup(func() {
		readyTimeout, probation, binPoll, livenessEvery, crashBackoffMax, drainTimeout =
			saved[0], saved[1], saved[2], saved[3], saved[4], saved[5]
	})
}

// installScript puts a shell script in bin/ as release v: a stand-in worker that
// says ready (or not) on TSP_READY_FD.
func installScript(t *testing.T, dir, v, body string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "tsp")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installProxyBin(dir, src, v); err != nil {
		t.Fatal(err)
	}
}

const (
	scriptReady      = `eval "echo ready >&$TSP_READY_FD"; exec sleep 600`
	scriptFails      = `eval "echo 'fail the CA is missing' >&$TSP_READY_FD"; exit 1`
	scriptNeverReady = `exec sleep 600`
	scriptDiesSoon   = `eval "echo ready >&$TSP_READY_FD"; sleep 0.3; exit 3`
)

// installSelf puts this test binary in bin/ as release v: a real tsp worker.
func installSelf(t *testing.T, dir, v string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TSP_TEST_AS_TSP", "1") // the workers it starts are tsp, not the tests
	if err := installProxyBin(dir, exe, v); err != nil {
		t.Fatal(err)
	}
}

func superviseFor(t *testing.T) (dir string, ln net.Listener) {
	t.Helper()
	unixOnly(t)
	home, _ := isolate(t)
	fastSupervision(t)
	withVersion(t, "1.14.0")
	dir = filepath.Join(home, ".tsp")
	if err := os.MkdirAll(binDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return dir, ln
}

// runSupervisorFor starts the supervisor; stop ends it and waits.
func runSupervisorFor(t *testing.T, dir string, ln net.Listener) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runSupervisor(ctx, sockets{tls: []net.Listener{ln}, port: 443}) }()
	stopped := false
	stop = func() {
		if !stopped {
			stopped = true
			cancel()
			<-done
		}
	}
	t.Cleanup(stop)
	return stop
}

// waitState waits for proxy.json to satisfy ok.
func waitState(t *testing.T, dir string, what string, ok func(proxyState) bool) proxyState {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, running := runningProxy(dir)
		if running && ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s: proxy.json = %+v (running %v)", what, st, running)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSupervisor_startsTheCurrentRelease(t *testing.T) {
	dir, ln := superviseFor(t)
	installScript(t, dir, "1.14.0", scriptReady)
	runSupervisorFor(t, dir, ln)
	st := waitState(t, dir, "the worker", func(st proxyState) bool { return st.Mode == "worker" })
	if st.Version != "1.14.0" || st.Worker == st.PID || st.Launcher != "1.14.0" {
		t.Errorf("proxy.json = %+v", st)
	}
}

func TestSupervisor_swapsToANewerReleaseOnceReady(t *testing.T) {
	dir, ln := superviseFor(t)
	installScript(t, dir, "1.14.0", scriptReady)
	runSupervisorFor(t, dir, ln)
	first := waitState(t, dir, "1.14.0", func(st proxyState) bool { return st.Version == "1.14.0" })
	installScript(t, dir, "1.15.0", scriptReady)
	st := waitState(t, dir, "the swap", func(st proxyState) bool { return st.Version == "1.15.0" })
	if st.Swaps != 1 || st.Worker == first.Worker || st.Rollbacks != 0 {
		t.Errorf("after the swap proxy.json = %+v", st)
	}
	if err := processAlive(first.Worker); err == nil {
		time.Sleep(500 * time.Millisecond)
		if processAlive(first.Worker) == nil {
			t.Error("the old worker still runs after the swap")
		}
	}
}

func TestSupervisor_aReleaseThatIsNotReadyIsSetAside(t *testing.T) {
	for name, script := range map[string]string{"fails": scriptFails, "never ready": scriptNeverReady} {
		t.Run(name, func(t *testing.T) {
			dir, ln := superviseFor(t)
			if name == "never ready" {
				readyTimeout = 300 * time.Millisecond
			}
			installScript(t, dir, "1.14.0", scriptReady)
			runSupervisorFor(t, dir, ln)
			first := waitState(t, dir, "1.14.0", func(st proxyState) bool { return st.Version == "1.14.0" })
			installScript(t, dir, "1.15.0", script)
			st := waitState(t, dir, "the rollback", func(st proxyState) bool { return st.Rollbacks == 1 })
			if st.Version != "1.14.0" || st.Worker != first.Worker || st.Swaps != 0 {
				t.Errorf("the old release stopped serving: %+v", st)
			}
			if !isBadVersion(dir, "1.15.0") || proxyBinVersion(dir) != "1.14.0" {
				t.Errorf("bin/state.json = %+v", loadBinState(dir))
			}
			time.Sleep(10 * binPoll) // a bad release is never tried again
			if st2, _ := runningProxy(dir); st2.Rollbacks != 1 {
				t.Errorf("tried the bad release again: %+v", st2)
			}
		})
	}
}

func TestSupervisor_aReleaseThatDiesInProbationIsRolledBack(t *testing.T) {
	dir, ln := superviseFor(t)
	installScript(t, dir, "1.14.0", scriptReady)
	runSupervisorFor(t, dir, ln)
	waitState(t, dir, "1.14.0", func(st proxyState) bool { return st.Version == "1.14.0" })
	installScript(t, dir, "1.15.0", scriptDiesSoon)
	st := waitState(t, dir, "the rollback to 1.14.0", func(st proxyState) bool { return st.Rollbacks == 1 && st.Version == "1.14.0" && st.Mode == "worker" })
	if !isBadVersion(dir, "1.15.0") || st.Swaps != 1 {
		t.Errorf("proxy.json = %+v, bin = %+v", st, loadBinState(dir))
	}
}

func TestSupervisor_servesItselfWhenNoCopyRuns(t *testing.T) {
	dir, ln := superviseFor(t)
	installScript(t, dir, "1.14.0", scriptFails)
	runSupervisorFor(t, dir, ln)
	st := waitState(t, dir, "the built-in server", func(st proxyState) bool { return st.Mode == "builtin" })
	if st.Worker != st.PID || st.Rollbacks != 1 {
		t.Errorf("proxy.json = %+v", st)
	}
	// It still serves the names: the health name answers through the port.
	if body, err := getHTTPS(dir, ln.Addr().String(), healthName); err != nil || !strings.HasPrefix(body, "ok ") {
		t.Errorf("built-in server: %q, %v", body, err)
	}
	// A good release later takes over from the built-in server.
	installScript(t, dir, "1.15.0", scriptReady)
	waitState(t, dir, "the worker", func(st proxyState) bool { return st.Mode == "worker" && st.Version == "1.15.0" })
}

func TestSupervisor_restartsACrashedWorkerWithBackoff(t *testing.T) {
	dir, ln := superviseFor(t)
	probation = 50 * time.Millisecond // past probation: a crash, not a failed update
	installScript(t, dir, "1.14.0", `eval "echo ready >&$TSP_READY_FD"; sleep 0.4; exit 2`)
	runSupervisorFor(t, dir, ln)
	st := waitState(t, dir, "three crashes, then the rollback", func(st proxyState) bool { return st.Crashes >= 3 && st.Rollbacks >= 1 })
	if !isBadVersion(dir, "1.14.0") || st.Mode != "builtin" {
		t.Errorf("after crashing three times in five minutes: %+v (bad %v)", st, loadBinState(dir).Bad)
	}
}

func TestSupervisor_refusesADowngrade(t *testing.T) {
	dir, ln := superviseFor(t)
	installScript(t, dir, "1.13.0", scriptReady) // older than this launcher (1.14.0)
	runSupervisorFor(t, dir, ln)
	st := waitState(t, dir, "the built-in server", func(st proxyState) bool { return st.Mode == "builtin" })
	if st.Version != "1.14.0" {
		t.Errorf("proxy.json = %+v", st)
	}
}

// TestSupervisor_swapDropsNoRequest swaps between two real tsp workers while a
// client keeps sending requests through the shared socket: none may fail.
func TestSupervisor_swapDropsNoRequest(t *testing.T) {
	dir, ln := superviseFor(t)
	reg := newLocalRegistry(filepath.Join(dir, "routes"))
	running(t, reg, localRoute{Name: "api", Port: backend(t), PID: os.Getpid(), Dir: "/src/api"})
	installSelf(t, dir, "1.14.0")
	runSupervisorFor(t, dir, ln)
	waitState(t, dir, "the first worker", func(st proxyState) bool { return st.Mode == "worker" })

	var ok, failed atomic.Int64
	var lastErr atomic.Value
	stopLoad := make(chan struct{})
	loadDone := make(chan struct{})
	go func() {
		defer close(loadDone)
		for {
			select {
			case <-stopLoad:
				return
			default:
			}
			if body, err := getHTTPS(dir, ln.Addr().String(), "api"); err == nil && body == "GET /hello" {
				ok.Add(1)
			} else {
				failed.Add(1)
				lastErr.Store(fmt.Sprint(body, err))
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)
	installSelf(t, dir, "1.15.0")
	waitState(t, dir, "the swap", func(st proxyState) bool { return st.Version == "1.15.0" && st.Swaps == 1 })
	time.Sleep(300 * time.Millisecond)
	close(stopLoad)
	<-loadDone
	if failed.Load() != 0 || ok.Load() < 10 {
		t.Errorf("during the swap: %d ok, %d failed (last: %v)", ok.Load(), failed.Load(), lastErr.Load())
		if data, err := os.ReadFile(proxyLogPath(dir)); err == nil {
			t.Logf("proxy.jsonl:\n%s", data)
		}
	}
}

// getHTTPS GETs https://<name>.localhost/hello through addr, trusting dir's CA.
func getHTTPS(dir, addr, name string) (string, error) {
	ca, err := loadCA(caFiles(dir))
	if err != nil {
		return "", err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DisableKeepAlives: true, // every request a new connection: each one is accepted by whoever serves now
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
	}}
	resp, err := client.Get("https://" + name + ".localhost/hello")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func processAlive(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscallZero)
}

func TestSupervisor_redirectsHTTPThroughTheWorker(t *testing.T) {
	dir, ln := superviseFor(t)
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpLn.Close() })
	installSelf(t, dir, "1.14.0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- runSupervisor(ctx, sockets{tls: []net.Listener{ln}, http: []net.Listener{httpLn}, port: 443})
	}()
	t.Cleanup(func() { cancel(); <-done })
	st := waitState(t, dir, "the worker", func(st proxyState) bool { return st.Mode == "worker" })
	if st.HTTPPort != localHTTPPort {
		t.Errorf("proxy.json httpPort = %d", st.HTTPPort)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", "http://"+httpLn.Addr().String()+"/a?b=1", nil)
	req.Host = "tsp.localhost" // the dashboard: on by default, unlike an unknown name (the catch-all is off)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "https://tsp.localhost/a?b=1" {
		t.Errorf("http: %s %q", resp.Status, resp.Header.Get("Location"))
	}
}
