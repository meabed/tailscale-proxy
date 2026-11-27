package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// `tsp proxy` serves https://<name>.localhost on 127.0.0.1:443 and [::1]:443,
// routing by host to the port `tsp run` registered. Three ways it runs:
//   - the boot service (--uid/--gid/--home, as root): it binds 443, drops to the
//     user for good and stays as the supervisor, holding the sockets and running
//     the user's own tsp as its worker (supervisor_unix.go);
//   - a worker the supervisor started (the sockets inherited, TSP_READY_FD set);
//   - in a terminal (`tsp proxy --port 8443`): it serves by itself.

const localProxyPort = 443

type localRouteKey struct{}

// healthName is the name a proxy answers itself, for its readiness check.
const healthName = "tsp-health"

// newLocalHandler proxies <name>.localhost to the registered app port. The app
// sees the original Host (its configured URL) and X-Forwarded-Proto: https. Every
// response is counted in m, and logged per requestLog ("errors", "all", "off").
func newLocalHandler(reg *localRegistry, m *proxyMetrics, requestLog string) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport:     newUpstreamTransport(),
		BufferPool:    copyBuffers,
		FlushInterval: -1, // SSE, streaming, chunked
		Rewrite: func(pr *httputil.ProxyRequest) {
			rt := pr.In.Context().Value(localRouteKey{}).(localRoute)
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(rt.Port))
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded() // X-Forwarded-For, -Host (the .localhost name), -Proto https
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			rt := r.Context().Value(localRouteKey{}).(localRoute)
			reg.forget(rt)
			if mw, ok := w.(*meteredWriter); ok {
				mw.failed = true
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "tsp: %s is registered on port %d but not answering: %v\nIs its dev server still starting? Check the terminal running `tsp run` (%s).\n", rt.URL(), rt.Port, err, rt.Dir)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := nameFromHost(r.Host)
		if name == healthName {
			_, _ = io.WriteString(w, "ok "+Version+"\n")
			return
		}
		mw := newMeteredWriter(w)
		rt, ok := lookupWithParents(reg, name)
		key := "" // metrics key: a registered name only, never the request's Host
		if ok {
			proxy.ServeHTTP(mw, r.WithContext(context.WithValue(r.Context(), localRouteKey{}, rt)))
			key = rt.Name
		} else {
			writeLocalIndex(mw, reg, name)
		}
		status, ttfb := mw.result()
		m.record(key, status, ttfb, mw.bytes, mw.failed)
		logRequestEvent(requestLog, key, r, status, ttfb, mw.failed)
	})
}

// lookupWithParents resolves name, else its nearest registered parent:
// acme.help-chat → help-chat, so one app serves every <tenant>.<name>.localhost.
func lookupWithParents(reg *localRegistry, name string) (localRoute, bool) {
	for n := name; n != ""; _, n, _ = strings.Cut(n, ".") {
		if rt, ok := reg.lookup(n); ok {
			return rt, true
		}
	}
	return localRoute{}, false
}

// writeLocalIndex answers an unknown name with 404 and the names that are running.
func writeLocalIndex(w http.ResponseWriter, reg *localRegistry, name string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if name != "" {
		fmt.Fprintf(w, "tsp: nothing runs at https://%s.%s — start it with `tsp run` (bun dev) in its folder.\n\n", name, localTLD)
	}
	routes := reg.list()
	if len(routes) == 0 {
		fmt.Fprintln(w, "No apps are running. `tsp run` in an app's folder registers its https://<name>.localhost.")
		return
	}
	fmt.Fprintln(w, "Running:")
	for _, rt := range routes {
		fmt.Fprintf(w, "  %s  →  127.0.0.1:%d  (%s)\n", rt.URL(), rt.Port, rt.Dir)
	}
}

// proxyState is ~/.tsp/proxy.json: which proxy serves this user's names. The
// supervisor (or a standalone `tsp proxy`) holds proxy.lock while it runs and
// rewrites the file on every change: a swap, a rollback, a fallback.
type proxyState struct {
	PID       int       `json:"pid"`                // the supervisor (or standalone proxy)
	Worker    int       `json:"worker"`             // the process serving now: the worker, else PID
	Version   string    `json:"version"`            // the release serving now
	Port      int       `json:"port"`               //
	Launcher  string    `json:"launcher,omitempty"` // the boot service's own release
	Mode      string    `json:"mode"`               // "worker", "builtin" (the launcher serves), "standalone"
	Started   time.Time `json:"started"`            // when the supervisor started
	Swaps     int       `json:"swaps"`              // live moves to a newer release
	Rollbacks int       `json:"rollbacks"`          // releases that failed and were set aside
	Crashes   int       `json:"crashes"`            // workers that exited on their own
}

// outdated reports whether the running proxy is an older tsp than this one.
func (st proxyState) outdated() bool { return Version != "dev" && versionLess(st.Version, Version) }

func proxyStatePath(dir string) string { return filepath.Join(dir, "proxy.json") }
func proxyLockPath(dir string) string  { return filepath.Join(dir, "proxy.lock") }

// runningProxy returns the live proxy's state, if one is running for this user.
func runningProxy(dir string) (proxyState, bool) {
	release, free, err := tryLock(proxyLockPath(dir))
	if err != nil {
		return proxyState{}, false
	}
	if free {
		release()
		return proxyState{}, false
	}
	var st proxyState
	data, err := os.ReadFile(proxyStatePath(dir))
	if err != nil || json.Unmarshal(data, &st) != nil {
		return proxyState{}, false
	}
	return st, true
}

// listenLoopback binds port on 127.0.0.1 and, where the host has IPv6, on [::1]
// (browsers resolve *.localhost to either).
func listenLoopback(port int) ([]net.Listener, error) {
	p := strconv.Itoa(port)
	v4, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", p))
	if err != nil {
		return nil, err
	}
	lns := []net.Listener{v4}
	if v6, err := net.Listen("tcp", net.JoinHostPort("::1", p)); err == nil {
		lns = append(lns, v6)
	} else if !errors.Is(err, syscall.EADDRNOTAVAIL) && !errors.Is(err, syscall.EAFNOSUPPORT) {
		_ = v4.Close()
		return nil, err
	}
	return lns, nil
}

func proxyUsage() {
	fmt.Print(`tsp proxy — serve https://<name>.localhost for the apps "tsp run" starts

Usage:
  tsp proxy [flags]     (the boot service runs this; see "tsp service install")

Flags:
  --port <n>     HTTPS port on 127.0.0.1 and [::1]   (default 443, needs root)
  --uid <n>      Started as root: run as this user after binding the port
  --gid <n>      That user's group
  --home <dir>   That user's home (where ~/.tsp lives)
`)
}

func cmdProxy(argv []string) int {
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	fs.Usage = proxyUsage
	port := fs.Int("port", localProxyPort, "HTTPS port")
	uid := fs.Int("uid", -1, "user to run as after binding")
	gid := fs.Int("gid", -1, "group to run as after binding")
	home := fs.String("home", "", "the user's home")
	if err := fs.Parse(argv); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if (*uid >= 0) != (*gid >= 0) || (*uid >= 0 && *home == "") {
		fmt.Fprintln(os.Stderr, "--uid, --gid and --home go together")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	inh, err := inheritedSockets()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsp proxy:", err)
		return 1
	}
	if inh.ok {
		return runWorker(ctx, inh)
	}
	lns, err := listenLoopback(*port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot listen on 127.0.0.1:%d: %v\n", *port, err)
		if *port < 1024 && os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr, "Ports below 1024 need root: install the boot service with `tsp service install`.")
		} else {
			fmt.Fprintln(os.Stderr, "Another proxy holds it (a second tsp proxy?): `lsof -nP -iTCP:443 -sTCP:LISTEN` shows which.")
		}
		return 1
	}
	if *uid >= 0 {
		if err := dropPrivileges(*uid, *gid); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_ = os.Setenv("HOME", *home)
		return runSupervisor(ctx, lns, *port)
	}
	return runStandalone(ctx, lns, *port)
}

// drainTimeout bounds how long a stopping proxy finishes the requests in flight.
var drainTimeout = 10 * time.Second

// localServer is the HTTPS proxy for *.localhost, ready to serve on any listeners.
type localServer struct {
	dir     string
	ca      *localCA
	srv     *http.Server
	metrics *proxyMetrics
	lns     []net.Listener // what serve accepts on: shutdown closes them first
}

func newLocalServer(dir string) (*localServer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ca, err := ensureCA(dir)
	if err != nil {
		return nil, err
	}
	iss, err := newCertIssuer(ca)
	if err != nil {
		return nil, err
	}
	m := newProxyMetrics()
	iss.onIssue = func() { m.certs.Add(1) }
	cfg, _, _, _ := LoadConfig()
	srv := newServer("", newLocalHandler(newLocalRegistry(filepath.Join(dir, "routes")), m, cfg.LocalRequestLog))
	srv.TLSConfig = &tls.Config{GetCertificate: iss.GetCertificate, MinVersion: tls.VersionTLS12}
	srv.ErrorLog = log.New(io.Discard, "", 0) // per-connection TLS noise: an untrusted CA, a port scan
	srv.ConnState = m.connState
	return &localServer{dir: dir, ca: ca, srv: srv, metrics: m}, nil
}

// serve accepts on lns; the channel gets each listener's end.
func (ls *localServer) serve(lns []net.Listener) <-chan error {
	ls.lns = append(ls.lns, lns...)
	errc := make(chan error, len(lns))
	for _, ln := range lns {
		go func(ln net.Listener) { errc <- ls.srv.ServeTLS(ln, "", "") }(ln)
	}
	return errc
}

// selfCheck proves this proxy serves before it takes traffic: a real HTTPS request
// to its own health name, through a private listener, its TLS verified against the
// local CA (so the CA loads, certificates are issued and the handler answers).
func (ls *localServer) selfCheck(ctx context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	go func() { errc <- ls.srv.ServeTLS(ln, "", "") }()
	defer ln.Close()
	pool := x509.NewCertPool()
	pool.AddCert(ls.ca.cert)
	addr := ln.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: healthName + "." + localTLD},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+healthName+"."+localTLD+"/", nil)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("self-check: %v", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "ok ") {
		return fmt.Errorf("self-check: %s %q", resp.Status, body)
	}
	return nil
}

// shutdown stops accepting and finishes the requests in flight, for up to d, the
// way a graceful proxy drains: it closes its listeners but keeps serving, so a
// connection accepted a moment before (its TLS handshake and first request still
// coming) gets its answer; once no connection is new or mid-request, Shutdown
// closes the idle keep-alive ones. (Shutdown first would drop that connection: a
// shutting-down http.Server closes one whose first request arrives after it.)
func (ls *localServer) shutdown(d time.Duration) {
	deadline := time.Now().Add(d)
	for _, ln := range ls.lns {
		_ = ln.Close()
	}
	for quiet := 0; quiet < 2 && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		if ls.metrics.busy.Load() <= 0 {
			quiet++
		} else {
			quiet = 0
		}
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	_ = ls.srv.Shutdown(ctx)
}

// runStandalone is `tsp proxy` in a terminal: it serves by itself, holding the
// proxy lock and writing proxy.json like the supervisor does.
func runStandalone(ctx context.Context, lns []net.Listener, port int) int {
	dir, err := stateDir()
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	initAppLog(dir, "proxy")
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
	ls, err := newLocalServer(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	st := proxyState{PID: os.Getpid(), Worker: os.Getpid(), Version: Version, Port: port, Mode: "standalone", Started: time.Now().UTC()}
	if err := writeProxyState(dir, st); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.Remove(proxyStatePath(dir))
	errc := ls.serve(lns)
	go adminServer(ctx, dir, func() *proxyMetrics { return ls.metrics })
	go historyLoop(ctx, dir, "local", ls.metrics)
	appLog().Info("proxy started", "port", port, "mode", "standalone")
	log.Printf("tsp proxy %s: https://*.%s on port %d (CA %s)", Version, localTLD, port, ls.ca.certPEM)
	code := 0
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("proxy stopped: %v", err)
			code = 1
		}
	case <-ctx.Done():
	}
	ls.shutdown(drainTimeout)
	appLog().Info("proxy stopped")
	return code
}

func writeProxyState(dir string, st proxyState) error {
	data, _ := json.Marshal(st)
	return writeFileAtomic(proxyStatePath(dir), data, 0o644)
}
