package core

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// backend starts an app that echoes what it received.
func backend(t *testing.T) (port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Host", r.Host)
		w.Header().Set("X-Seen-Proto", r.Header.Get("X-Forwarded-Proto"))
		w.Header().Set("X-Seen-Fwd-Host", r.Header.Get("X-Forwarded-Host"))
		io.WriteString(w, r.Method+" "+r.URL.RequestURI())
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func TestLocalHandler_routesByHost(t *testing.T) {
	reg := testRegistry(t)
	running(t, reg, LocalRoute{Name: "api.acme", Port: backend(t), PID: 1, Dir: "/src/api"})
	h := newLocalHandler(reg)

	req := httptest.NewRequest("GET", "https://api.acme.localhost/v1/items?q=1", nil)
	req.Host = "api.acme.localhost"
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "GET /v1/items?q=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Seen-Host"); got != "api.acme.localhost" {
		t.Errorf("app saw Host %q, want the .localhost name", got)
	}
	if got := rec.Header().Get("X-Seen-Proto"); got != "https" {
		t.Errorf("app saw X-Forwarded-Proto %q, want https", got)
	}
	if got := rec.Header().Get("X-Seen-Fwd-Host"); got != "api.acme.localhost" {
		t.Errorf("app saw X-Forwarded-Host %q", got)
	}
}

func TestLocalHandler_subdomainsReachTheirParent(t *testing.T) {
	reg := testRegistry(t)
	running(t, reg, LocalRoute{Name: "help-chat", Port: backend(t), PID: 1})
	h := newLocalHandler(reg)
	for host, want := range map[string]int{"acme.help-chat.localhost": 200, "a.b.help-chat.localhost": 200, "help-chat.localhost": 200, "other.localhost": 404, "chat.localhost": 404} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s → %d, want %d", host, rec.Code, want)
		}
		if want == 200 && rec.Header().Get("X-Seen-Host") != host {
			t.Errorf("%s: the app saw Host %q", host, rec.Header().Get("X-Seen-Host"))
		}
	}
}

func TestLocalHandler_unknownNameLists(t *testing.T) {
	reg := testRegistry(t)
	running(t, reg, LocalRoute{Name: "web", Port: 4100, PID: 1, Dir: "/src/web"})
	h := newLocalHandler(reg)

	for host, want := range map[string]string{
		"nope.localhost": "nothing runs at https://nope.localhost",
		"localhost":      "https://web.localhost  →  127.0.0.1:4100  (/src/web)",
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: got %d %q, want 404 containing %q", host, rec.Code, rec.Body.String(), want)
		}
	}

	empty := newLocalHandler(testRegistry(t))
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "localhost"
	rec := httptest.NewRecorder()
	empty.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "No apps are running") {
		t.Errorf("empty registry page = %q", rec.Body.String())
	}
}

func TestLocalHandler_deadAppIs502AndForgotten(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // nothing listens there now

	reg := testRegistry(t)
	stop := running(t, reg, LocalRoute{Name: "gone", Port: port, PID: 7, Dir: "/src/gone"})
	h := newLocalHandler(reg)
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "gone.localhost"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "not answering") {
		t.Fatalf("got %d %q, want 502", rec.Code, rec.Body.String())
	}
	if _, ok := liveRoute(reg, "gone"); !ok {
		t.Fatal("a live run's route was dropped on one failed request")
	}
	stop() // its run ends
	h.ServeHTTP(httptest.NewRecorder(), req)
	if _, err := os.Stat(reg.file("gone")); !os.IsNotExist(err) {
		t.Error("the route of a dead run was kept after it stopped answering")
	}
}

func TestLocalHandler_websocketUpgrade(t *testing.T) {
	// An app that accepts an Upgrade and echoes one line over the raw connection.
	app, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { app.Close() })
	go func() {
		c, err := app.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		line, _ := br.ReadString('\n')
		io.WriteString(c, "echo:"+line)
	}()

	reg := testRegistry(t)
	running(t, reg, LocalRoute{Name: "hmr", Port: app.Addr().(*net.TCPAddr).Port, PID: 1})
	front := httptest.NewServer(newLocalHandler(reg))
	t.Cleanup(front.Close)

	c, err := net.Dial("tcp", front.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "GET /_hmr HTTP/1.1\r\nHost: hmr.localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 101 {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	io.WriteString(c, "ping\n")
	if got, _ := br.ReadString('\n'); got != "echo:ping\n" {
		t.Errorf("over the upgraded connection got %q", got)
	}
}

func TestServeLocal_endToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".tailscale-proxy")
	reg := newLocalRegistry(filepath.Join(dir, "routes"))
	running(t, reg, LocalRoute{Name: "api", Port: backend(t), PID: os.Getpid(), Dir: "/src/api"})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- serveLocal(ctx, []net.Listener{ln}, 443) }()

	var st proxyState
	for i := 0; i < 100; i++ {
		if s, ok := runningProxy(dir); ok {
			st = s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.PID != os.Getpid() || st.Port != 443 || st.Version != Version {
		t.Fatalf("proxy.json = %+v", st)
	}
	ca, err := loadCA(caFiles(dir))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
	}}
	resp, err := client.Get("https://api.localhost/hello")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "GET /hello" || resp.ProtoMajor != 2 {
		t.Errorf("got %d %s %q, want 200 over HTTP/2", resp.StatusCode, resp.Proto, body)
	}

	// A name outside .localhost gets no certificate.
	if _, err := client.Get("https://example.com/"); err == nil {
		t.Error("the proxy completed a TLS handshake for example.com")
	}

	cancel()
	if code := <-done; code != 0 {
		t.Errorf("serveLocal exit = %d", code)
	}
	if _, err := os.Stat(proxyStatePath(dir)); !os.IsNotExist(err) {
		t.Error("proxy.json was left behind after shutdown")
	}
}

func TestRunningProxy(t *testing.T) {
	dir := t.TempDir()
	if _, ok := runningProxy(dir); ok {
		t.Fatal("runningProxy with nothing there = true")
	}
	data, _ := json.Marshal(proxyState{PID: 42, Version: "1.2.3", Port: 443})
	_ = os.WriteFile(proxyStatePath(dir), data, 0o644)
	if _, ok := runningProxy(dir); ok {
		t.Fatal("runningProxy = true from a leftover proxy.json without its lock held")
	}
	release, ok, err := tryLock(proxyLockPath(dir))
	if err != nil || !ok {
		t.Fatal(err)
	}
	defer release()
	if st, ok := runningProxy(dir); !ok || st.Version != "1.2.3" || st.PID != 42 {
		t.Errorf("runningProxy = %+v, %v", st, ok)
	}
}

func TestServeLocal_secondProxyRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".tailscale-proxy")
	_ = os.MkdirAll(dir, 0o755)
	release, _, _ := tryLock(proxyLockPath(dir))
	defer release()
	if code := serveLocal(context.Background(), nil, 443); code != 1 {
		t.Errorf("a second proxy for the same user = %d, want 1", code)
	}
}

func TestListenLoopback(t *testing.T) {
	probe, _ := net.Listen("tcp", "127.0.0.1:0")
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	lns, err := listenLoopback(port)
	if err != nil {
		t.Fatal(err)
	}
	for _, ln := range lns {
		if !strings.HasSuffix(ln.Addr().String(), ":"+strconv.Itoa(port)) {
			t.Errorf("listener on %s, want port %d", ln.Addr(), port)
		}
	}
	if _, err := listenLoopback(port); err == nil {
		t.Error("a second listenLoopback on the same port succeeded")
	}
	for _, ln := range lns {
		_ = ln.Close()
	}
}

func TestCmdProxy_flags(t *testing.T) {
	if code := cmdProxy([]string{"--uid", "501"}); code != 2 {
		t.Errorf("--uid without --gid/--home = %d, want 2", code)
	}
	if code := cmdProxy([]string{"--nope"}); code != 2 {
		t.Errorf("unknown flag = %d, want 2", code)
	}
	if code := cmdProxy([]string{"-h"}); code != 0 {
		t.Errorf("-h = %d, want 0", code)
	}
	busy, _ := net.Listen("tcp", "127.0.0.1:0")
	defer busy.Close()
	if code := cmdProxy([]string{"--port", strconv.Itoa(busy.Addr().(*net.TCPAddr).Port)}); code != 1 {
		t.Errorf("busy port = %d, want 1", code)
	}
}
