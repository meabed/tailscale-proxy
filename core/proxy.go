package core

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// splitFirstSegment splits "/seg/rest..." into ("seg", "/rest...").
// "/seg" and "/seg/" both yield ("seg", "/"); "/" and "" yield ("", "/").
func splitFirstSegment(p string) (seg, rest string) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", "/"
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], "/" + p[i+1:]
	}
	return p, "/"
}

// writeIndex answers an unknown path with 404 and the registered services.
func writeIndex(w http.ResponseWriter, store *routeStore) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	snap := store.snapshot()
	fmt.Fprintln(w, "tailscale-proxy — registered services:")
	if len(snap) == 0 {
		fmt.Fprintln(w, "  (none discovered — start a dev server in range, or try --all / --ports, then `tsp doctor`)")
		return
	}
	for _, slug := range sortedSlugs(snap) {
		svc := snap[slug]
		fmt.Fprintf(w, "  /%s/  →  %s  (%s)\n", slug, targetDisplay(svc), runtimeOr(svc.Runtime))
	}
}

type targetKey struct{}

func (s Service) upstreamHost() string {
	if s.Host != "" {
		return s.Host
	}
	return "127.0.0.1"
}

// target is the resolved upstream for a single request.
type target struct {
	host string
	port int    // upstream port
	path string // rewritten path with the matched segment stripped
}

func (t target) dialHost() string { return t.host + ":" + strconv.Itoa(t.port) }

// hostHeader is the Host the app sees. We use "localhost" (not the raw IP)
// because dev servers, CORS origins, and cookies are keyed to how developers
// actually reach them — `http://localhost:<port>`.
func (t target) hostHeader() string { return "localhost:" + strconv.Itoa(t.port) }

// copyBuffers recycles the reverse proxy's 32 KiB body-copy buffers instead of
// allocating one per request.
var copyBuffers = &bufferPool{}

type bufferPool struct{ p sync.Pool }

func (b *bufferPool) Get() []byte {
	if buf, ok := b.p.Get().(*[]byte); ok {
		return *buf
	}
	return make([]byte, 32*1024)
}

func (b *bufferPool) Put(buf []byte) { b.p.Put(&buf) }

// newUpstreamTransport is the bounded transport both proxies dial apps with: the
// idle pool is sized for a page load's burst of parallel asset requests (a small
// per-host cap forced constant re-dials under load), and the idle timeout drops
// connections to dev servers that come and go, so memory stays flat.
func newUpstreamTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// newHandler returns an HTTP handler that routes by first path segment.
// When logRequests is true, each request is logged with method, status,
// target, and duration. When forwardHost is true, the external (funnel/serve)
// host is forwarded to the app via X-Forwarded-Host + X-Forwarded-Proto=https;
// otherwise the app only ever sees the local host (so it behaves like localhost).
// Every response is counted in m, by project slug.
func newHandler(store *routeStore, logRequests, forwardHost bool, m *proxyMetrics) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport:     newUpstreamTransport(),
		BufferPool:    copyBuffers,
		FlushInterval: -1, // flush immediately: SSE / streaming / chunked
		Rewrite: func(pr *httputil.ProxyRequest) {
			tgt := pr.In.Context().Value(targetKey{}).(target)
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = tgt.dialHost()
			pr.Out.URL.Path = tgt.path
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			// Present the request as "localhost:<port>" so it's indistinguishable
			// from how the developer normally reaches the server.
			pr.Out.Host = tgt.hostHeader()
			// Keeps X-Forwarded-For (real client IP); also sets X-Forwarded-Host to
			// the inbound (external) host, which we override below.
			pr.SetXForwarded()
			if forwardHost {
				// Let the app know the public URL it's served at.
				pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
				pr.Out.Header.Set("X-Forwarded-Proto", "https")
			} else {
				// Present a purely local request — never leak the external host, so
				// the app builds URLs/redirects exactly as it would on localhost.
				pr.Out.Header.Set("X-Forwarded-Host", tgt.hostHeader())
				pr.Out.Header.Set("X-Forwarded-Proto", "http")
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if mw, ok := w.(*meteredWriter); ok {
				mw.failed = true
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "tailscale-proxy: upstream error: %v\n", err)
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		mw := newMeteredWriter(w)

		svc, path, ok := resolveRoute(store, r, w)
		if ok && svc.Share == "private" && r.Header.Get("Tailscale-Funnel-Request") != "" {
			// A project that says "share": "private" answers only the tailnet, even while
			// tsp runs public: Tailscale marks every Funnel request (and drops the header
			// a client sends itself).
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "tailscale-proxy: /%s/ is private to this tailnet\n", svc.Slug)
			return
		}
		if ok {
			tgt := target{host: svc.upstreamHost(), port: svc.Port, path: path}
			proxy.ServeHTTP(mw, r.WithContext(context.WithValue(r.Context(), targetKey{}, tgt)))
		} else {
			writeIndex(mw, store)
		}
		status, ttfb := mw.result()
		m.record(svc.Slug, status, ttfb, mw.bytes, mw.failed)

		if logRequests {
			logRequest(r, status, svc, time.Since(start))
		}
	})
}

// newServer wraps the proxy handler in an http.Server with header and idle
// timeouts, so stalled or abandoned client connections can't pile up over days.
// No read/write deadline: SSE, HMR and WebSocket streams stay open indefinitely.
func newServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

// routeCookie pins a browser to the last project it opened, so prefix-less
// asset/API requests (e.g. /_next/static/..., /api/...) — which lose the
// /<slug>/ prefix because the app assumes it lives at the root — still reach the
// right dev server. Without this, those requests 404 and the page renders broken.
const routeCookie = "tsp_route"

// resolveRoute determines the upstream port and rewritten path for a request.
// First path segment matching a slug wins (prefix stripped, affinity cookie set).
// Otherwise it falls back to the affinity cookie and forwards the full path.
// Returns (service, path, ok).
func resolveRoute(store *routeStore, r *http.Request, w http.ResponseWriter) (Service, string, bool) {
	seg, rest := splitFirstSegment(r.URL.Path)
	if seg != "" {
		if svc, ok := store.lookup(seg); ok {
			// Remember this app for subsequent prefix-less requests.
			http.SetCookie(w, &http.Cookie{
				Name: routeCookie, Value: seg, Path: "/", SameSite: http.SameSiteLaxMode,
			})
			return svc, rest, true
		}
	}
	// Prefix-less request (asset/API/HMR): follow the affinity cookie, forwarding
	// the full original path unchanged.
	if c, err := r.Cookie(routeCookie); err == nil && c.Value != "" {
		if svc, ok := store.lookup(c.Value); ok {
			return svc, r.URL.Path, true
		}
	}
	return Service{}, "", false
}

// logRequest prints one nicely formatted request line.
func logRequest(r *http.Request, status int, svc Service, dur time.Duration) {
	target := "—"
	if svc.Port > 0 {
		target = targetDisplay(svc)
	}
	log.Printf("%s %-6s %s → %s (%s)",
		colorStatus(status), r.Method, r.URL.Path, target, dur.Round(time.Millisecond))
}

// colorStatus renders an HTTP status code, colorized when writing to a terminal.
func colorStatus(code int) string {
	s := strconv.Itoa(code)
	if !useColor() {
		return s
	}
	var c string
	switch {
	case code >= 500:
		c = "\x1b[31m" // red
	case code >= 400:
		c = "\x1b[33m" // yellow
	case code >= 300:
		c = "\x1b[36m" // cyan
	default:
		c = "\x1b[32m" // green
	}
	return c + s + "\x1b[0m"
}

var (
	colorOnce    sync.Once
	colorEnabled bool
)

// useColor reports whether ANSI colors should be emitted (stderr is a TTY and
// NO_COLOR is unset). log output goes to stderr, so we probe stderr.
func useColor() bool {
	colorOnce.Do(func() {
		if os.Getenv("NO_COLOR") != "" {
			return
		}
		fi, err := os.Stderr.Stat()
		colorEnabled = err == nil && fi.Mode()&os.ModeCharDevice != 0
	})
	return colorEnabled
}
