//go:build !windows

package core

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startListener starts a helper dev server (this test binary) and returns it
// with its port; a stubborn one ignores SIGTERM.
func startListener(t *testing.T, stubborn bool) (*exec.Cmd, int) {
	t.Helper()
	mode := "plain"
	if stubborn {
		mode = "stubborn"
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "TSP_TEST_LISTEN="+mode)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }() // reaped, so a stopped one is gone, not a zombie
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	return cmd, port
}

// fromOwner lets the in-process test requests (from 192.0.2.1) stop apps.
func fromOwner(t *testing.T) {
	orig := requestFromOwner
	requestFromOwner = func(*http.Request) error { return nil }
	t.Cleanup(func() { requestFromOwner = orig })
}

func dashboardRequest(h http.Handler, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = dashboardName + "." + localTLD
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDashboardStop_showsThePlanThenStops(t *testing.T) {
	fromOwner(t)
	cmd, port := startListener(t, false)
	reg := testRegistry(t)
	running(t, reg, localRoute{Name: "web", Port: port, PID: cmd.Process.Pid, Dir: "/src/web"})
	h := newLocalHandler(t.TempDir(), reg, nil, newProxyMetrics(), "errors")

	rec := dashboardRequest(h, "GET", "/api/apps/web", "", nil)
	var plan stopPlan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); rec.Code != 200 || err != nil {
		t.Fatalf("plan: %d %v\n%s", rec.Code, err, rec.Body.String())
	}
	if plan.Target.PID != cmd.Process.Pid || plan.Runs != "kill -TERM "+strconv.Itoa(cmd.Process.Pid) || len(plan.Commands) < 3 || plan.Commands[0].Command != "tsp stop web" {
		t.Errorf("plan: %+v", plan)
	}

	rec = dashboardRequest(h, "POST", "/api/apps/web/stop", `{"force":false}`, map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"})
	var res stopResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); rec.Code != 200 || err != nil || !res.Stopped {
		t.Fatalf("stop: %d %v\n%s", rec.Code, err, rec.Body.String())
	}
	if pidRunning(cmd.Process.Pid) {
		t.Error("the app still runs")
	}
}

func TestDashboardStop_refusesOtherSitesAndForms(t *testing.T) {
	fromOwner(t)
	cmd, port := startListener(t, false)
	reg := testRegistry(t)
	running(t, reg, localRoute{Name: "web", Port: port, PID: cmd.Process.Pid, Dir: "/src/web"})
	h := newLocalHandler(t.TempDir(), reg, nil, newProxyMetrics(), "errors")

	for _, tc := range []struct {
		name   string
		header map[string]string
		code   int
	}{
		{"another site", map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}, http.StatusForbidden},
		{"an old browser", map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"}, http.StatusForbidden},
		{"a form", map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}, http.StatusUnsupportedMediaType},
	} {
		if rec := dashboardRequest(h, "POST", "/api/apps/web/stop", `{}`, tc.header); rec.Code != tc.code {
			t.Errorf("%s: %d, want %d\n%s", tc.name, rec.Code, tc.code, rec.Body.String())
		}
	}
	if rec := dashboardRequest(h, "GET", "/api/apps/web/stop", "", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET stop: %d", rec.Code)
	}
	if rec := dashboardRequest(h, "POST", "/api/apps/nope/stop", `{}`, map[string]string{"Content-Type": "application/json"}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown name: %d", rec.Code)
	}
	time.Sleep(50 * time.Millisecond)
	if !pidRunning(cmd.Process.Pid) {
		t.Error("a refused request stopped the app")
	}
}

func TestStopProcesses_forceEndsWhatIgnoresSIGTERM(t *testing.T) {
	orig := stopGrace
	stopGrace = 400 * time.Millisecond
	t.Cleanup(func() { stopGrace = orig })
	cmd, port := startListener(t, true)
	reg := testRegistry(t)
	running(t, reg, localRoute{Name: "stubborn", Port: port, PID: cmd.Process.Pid})
	rt, _ := reg.read("stubborn")
	rt.Source = sourceRun
	target, err := targetOf(reg, rt, &cpuSampler{})
	if err != nil {
		t.Fatal(err)
	}
	if res := stopProcesses(target, false); res.Stopped || !strings.Contains(res.Next, "tsp stop stubborn --force") {
		t.Fatalf("graceful: %+v", res)
	}
	if res := stopProcesses(target, true); !res.Stopped || !strings.HasPrefix(res.Ran, "kill -KILL ") {
		t.Fatalf("forced: %+v", res)
	}
}

func TestRequestFromOwner_onlyThisUsersProcesses(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	verdict := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verdict <- requestFromOwner(r)
	}))
	defer srv.Close()
	if out, err := exec.Command("curl", "-s", srv.URL).CombinedOutput(); err != nil {
		t.Fatalf("curl: %v %s", err, out)
	}
	if err := <-verdict; err != nil {
		t.Errorf("curl run by this user was refused: %v", err)
	}
	if err := requestFromOwner(httptest.NewRequest("POST", "/", nil)); err == nil { // from 192.0.2.1
		t.Error("a request from another machine was let through")
	}
	// A loopback address no connection of this user's has: nothing to attribute it to.
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	if err := requestFromOwner(req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443}))); err == nil {
		t.Error("a connection no process of this user holds was let through")
	}
}

func TestStopProcesses_forceEndsTheProcessesItStarted(t *testing.T) {
	orig := stopGrace
	stopGrace = 400 * time.Millisecond
	t.Cleanup(func() { stopGrace = orig })
	// A shell that starts a dev server and waits: SIGTERM ends the shell, and the
	// server it started keeps running until a forced stop ends the whole tree.
	sh := exec.Command("sh", "-c", `"$0" -test.run='^$' & wait`, os.Args[0])
	sh.Env = append(os.Environ(), "TSP_TEST_LISTEN=plain")
	out, err := sh.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sh.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = sh.Wait() }()
	if _, err := bufio.NewReader(out).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	target := stopTarget{Source: "pid", PID: sh.Process.Pid}
	target.inspect(&cpuSampler{})
	if len(target.Tree) != 1 || target.Process == nil || target.Process.Procs != 2 {
		t.Fatalf("target = %+v (process %+v)", target, target.Process)
	}
	child := target.Tree[0]
	t.Cleanup(func() { _ = signalStop(child, true) })
	if res := stopProcesses(target, false); res.Stopped {
		t.Fatalf("graceful stop reported the tree gone while its server runs: %+v", res)
	}
	if res := stopProcesses(target, true); !res.Stopped || !strings.Contains(res.Ran, strconv.Itoa(child)) {
		t.Fatalf("forced: %+v", res)
	}
}

func TestGuardTarget_neverStopsTspItself(t *testing.T) {
	for _, pid := range []int{0, 1, os.Getpid(), os.Getppid()} {
		if err := guardTarget(t.TempDir(), stopTarget{PID: pid}); err == nil {
			t.Errorf("pid %d was not refused", pid)
		}
	}
}

func TestDashboardStatus_marksAnExitedAppAndMeasuresALiveOne(t *testing.T) {
	cmd, port := startListener(t, false)
	reg := testRegistry(t)
	running(t, reg, localRoute{Name: "live", Port: port, PID: cmd.Process.Pid})
	running(t, reg, localRoute{Name: "gone", Port: 1, PID: 999_999})
	ds := newDashboardStatus(t.TempDir(), reg, nil, newProxyMetrics(), &cpuSampler{})
	by := map[string]appStatus{}
	for _, a := range ds.Apps {
		by[a.Name] = a
	}
	if a := by["live"]; a.Exited || a.Process == nil || a.Process.MemoryMB <= 0 || a.Process.Procs != 1 {
		t.Errorf("live = %+v (process %+v)", a, a.Process)
	}
	if a := by["gone"]; !a.Exited || a.Process != nil {
		t.Errorf("gone = %+v", a)
	}
}
