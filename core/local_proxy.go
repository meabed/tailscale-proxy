package core

import (
	"context"
	"crypto/tls"
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
// routing by host to the port `tsp run` registered. It is what the boot service
// runs: started as root to bind 443, it drops to the owner (--uid/--gid) before
// touching any file, so the CA and routes stay the user's.

const localProxyPort = 443

type localRouteKey struct{}

// newLocalHandler proxies <name>.localhost to the registered app port. The app
// sees the original Host (its configured URL) and X-Forwarded-Proto: https.
func newLocalHandler(reg *localRegistry) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport:     newUpstreamTransport(),
		BufferPool:    copyBuffers,
		FlushInterval: -1, // SSE, streaming, chunked
		Rewrite: func(pr *httputil.ProxyRequest) {
			rt := pr.In.Context().Value(localRouteKey{}).(LocalRoute)
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(rt.Port))
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded() // X-Forwarded-For, -Host (the .localhost name), -Proto https
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			rt := r.Context().Value(localRouteKey{}).(LocalRoute)
			reg.forget(rt)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "tsp: %s is registered on port %d but not answering: %v\nIs its dev server still starting? Check the terminal running `tsp run` (%s).\n", rt.URL(), rt.Port, err, rt.Dir)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := nameFromHost(r.Host)
		rt, ok := lookupWithParents(reg, name)
		if !ok {
			writeLocalIndex(w, reg, name)
			return
		}
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), localRouteKey{}, rt)))
	})
}

// lookupWithParents resolves name, else its nearest registered parent:
// acme.help-chat → help-chat, so one app serves every <tenant>.<name>.localhost.
func lookupWithParents(reg *localRegistry, name string) (LocalRoute, bool) {
	for n := name; n != ""; _, n, _ = strings.Cut(n, ".") {
		if rt, ok := reg.lookup(n); ok {
			return rt, true
		}
	}
	return LocalRoute{}, false
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

// proxyState is ~/.tailscale-proxy/proxy.json: which proxy serves this user's
// names. The proxy holds proxy.lock while it runs (see tryLock).
type proxyState struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
	Port    int    `json:"port"`
}

// outdated reports whether the running proxy is another tsp version than this one.
func (st proxyState) outdated() bool { return st.Version != Version && Version != "dev" }

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
  --home <dir>   That user's home (where ~/.tailscale-proxy lives)
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
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serveLocal(ctx, lns, *port)
}

// serveLocal runs the HTTPS proxy on lns until ctx ends.
func serveLocal(ctx context.Context, lns []net.Listener, port int) int {
	dir, err := stateDir()
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	release, ok, err := tryLock(proxyLockPath(dir))
	if err != nil || !ok {
		fmt.Fprintf(os.Stderr, "another tsp proxy already serves %s (tsp service status): %v\n", dir, err)
		return 1
	}
	defer release()
	ca, err := ensureCA(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	iss, err := newCertIssuer(ca)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	srv := newServer("", newLocalHandler(newLocalRegistry(filepath.Join(dir, "routes"))))
	srv.TLSConfig = &tls.Config{GetCertificate: iss.GetCertificate, MinVersion: tls.VersionTLS12}
	srv.ErrorLog = log.New(io.Discard, "", 0) // per-connection TLS noise: an untrusted CA, a port scan

	state, _ := json.Marshal(proxyState{PID: os.Getpid(), Version: Version, Port: port})
	if err := writeFileAtomic(proxyStatePath(dir), state, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.Remove(proxyStatePath(dir))

	errc := make(chan error, len(lns))
	for _, ln := range lns {
		go func(ln net.Listener) { errc <- srv.ServeTLS(ln, "", "") }(ln)
	}
	log.Printf("tsp proxy %s: https://*.%s on port %d (CA %s)", Version, localTLD, port, ca.certPEM)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("proxy stopped: %v", err)
			return 1
		}
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
	return 0
}
