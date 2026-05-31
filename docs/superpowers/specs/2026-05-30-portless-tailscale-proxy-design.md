# portless-tailscale-proxy — Design

**Date:** 2026-05-30
**Status:** Approved (pending spec review)
**Package / binary name:** `portless-tailscale-proxy` (bin alias: `ptp`)
**Language:** Go (standard library only)

## Purpose

Expose every locally-running [portless](https://portless.sh) dev server to the
public internet through a single [Tailscale Funnel](https://tailscale.com/kb/1223/funnel),
routed by URL path.

Tailscale Funnel can only expose **one** hostname (the node's MagicDNS name,
e.g. `bigfoot.quoll-adhara.ts.net`) on a fixed set of ports (443/8443/10000).
It cannot do wildcard subdomains. So we put a small path-routing reverse proxy
behind the Funnel: the first path segment of the URL is the exact portless
hostname, and it selects which local dev server to forward to.

```
                              ┌─────────────────────────────────────┐
  public internet             │  this machine                       │
                              │                                     │
  https://bigfoot.ts.net  ──► │  tailscale funnel (TLS, :443)       │
    /module-...-api.local/foo │        │  plain HTTP                 │
                              │        ▼                             │
                              │  ptp proxy (net/http, :8443)         │
                              │    map[module-...-api.local]         │
                              │        │ strip segment, → /foo       │
                              │        ▼                             │
                              │  127.0.0.1:4434  (real dev server)   │
                              └─────────────────────────────────────┘
                                        ▲ polled every 20s
                                  ~/.portless/routes.json
```

## Why Go

The goals are *tiny, least deps, single static binary, npx-easy, cross-platform*.
Go fits all of them:

- `net/http/httputil.ReverseProxy` gives **streaming + WebSocket upgrades +
  path rewrite** with the standard library only — no third-party proxy code.
- Compiles to a **single static binary (~6–8 MB)** with **zero runtime deps**.
- Cross-compiles to every target (macOS/Linux/Windows, amd64/arm64) from one
  machine via `GOOS`/`GOARCH`.
- Distributes through npm (for `npx`), Homebrew, `go install`, `curl | sh`, and
  GitHub Releases from the same build.

Formatting/lint use the Go toolchain (`gofmt`, `go vet`, optional
`golangci-lint`) — no extra config or dependencies.

## Supported platforms

| OS | Arch | Notes |
| --- | --- | --- |
| macOS | arm64, amd64 | |
| Linux | amd64, arm64 | |
| Windows | amd64, arm64 | `tailscale.exe`; paths via `os.UserHomeDir()` |
| WSL | (= Linux build) | runs the linux binary; needs `tailscale` reachable inside WSL |

Build matrix: `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`,
`windows/amd64`, `windows/arm64`.

## State source

Portless persists active routes at `~/.portless/routes.json`
(Windows: `%USERPROFILE%\.portless\routes.json`, resolved with `os.UserHomeDir()`):

```json
[
  { "hostname": "www-web-help-ai.local", "port": 4764, "pid": 4154 },
  { "hostname": "module-help-ai-agent-api.local", "port": 4434, "pid": 4315 }
]
```

We read and parse this file (overridable with `--state`). We do **not** shell out
to `portless list` — the JSON file is the stable machine-readable source of truth.

## Routing rules

For an incoming request with path `/<segment>/<rest...>?<query>`:

1. Take the **first** path segment verbatim (e.g. `module-help-ai-agent-api.local`).
   No slugification — it is the exact portless hostname.
2. Look it up in the live route map.
   - **Hit** → forward to `http://127.0.0.1:<port>/<rest...>?<query>`. The matched
     segment is **stripped**; the dev server sees its normal root path
     (`/module-help-ai-agent-api.local/foo?x=1` → backend gets `/foo?x=1`).
   - **Miss / empty path** → `404` with a plain-text body listing the currently
     registered services and their public URLs.

### Forwarding behavior (`httputil.ReverseProxy`)

- **Path rewrite + target:** a `Rewrite`/`Director` sets scheme `http`, host
  `127.0.0.1:<port>`, and trims the matched first segment from the path.
- **Host header:** rewritten to the target so dev servers that validate Host
  accept it. `X-Forwarded-Host` and `X-Forwarded-Proto: https` are added.
- **Streaming:** `FlushInterval = -1` so SSE / chunked responses flush
  immediately; request and response bodies stream (no buffering).
- **WebSockets:** `ReverseProxy` handles `Connection: Upgrade` / `101 Switching
  Protocols` natively — Vite/Next HMR and socket APIs work with no extra code.

## Preflight checks & guidance (`ptp doctor`, and auto-run on `start`)

Before starting, and via a dedicated `doctor` command, verify the environment and
print **actionable docs + links** for anything missing. Checks:

1. **Tailscale installed?** `tailscale version`. If absent → print the per-OS
   install link: <https://tailscale.com/download> (and note WSL specifics).
2. **Logged in / node up?** parse `tailscale status`. If logged out → `tailscale up`
   guidance.
3. **Funnel available for this tailnet?** `tailscale funnel status` /
   capability probe. Funnel requires (a) MagicDNS + HTTPS certificates enabled,
   and (b) the `funnel` node attribute granted in the tailnet policy file. If not
   enabled, surface the exact remediation links:
   - Enable Funnel / overview: <https://tailscale.com/kb/1223/funnel>
   - Enable HTTPS certificates: <https://tailscale.com/kb/1153/enabling-https>
   - Grant the `funnel` node attribute in the ACL policy file (admin console).
4. **portless installed & running?** check `portless` on PATH and that
   `routes.json` exists/parses. If missing → links:
   - <https://portless.sh> and `npm install -g portless`
   - `portless proxy start` to bring the local HTTPS proxy + state up.

`doctor` prints a check-list summary (✓ / ✗ with the fix link). `start` runs the
same checks; on a hard failure (no tailscale, Funnel disabled) it prints guidance
and exits non-zero unless `--no-funnel` is set (proxy-only mode still runs).

## Runtime & architecture (single small Go module)

| Unit | Responsibility | Stdlib used |
| --- | --- | --- |
| `loadRoutes(statePath)` | read + parse `routes.json` → `map[string]int` | `os`, `encoding/json` |
| `RouteStore` | holds current map behind a `sync.RWMutex`; `refresh()` swaps it; logs add/remove diffs | `sync` |
| `newProxy(store)` | one `httputil.ReverseProxy` whose `Rewrite` picks target per request from the store | `net/http/httputil` |
| `poll(ctx, interval)` | ticker that calls `store.refresh()` | `time`, `context` |
| `funnel` | `start`/`reset`/`status` wrapping `tailscale` | `os/exec` |
| `doctor` | environment checks + guidance links | `os/exec` |
| `main` / `cli` | subcommand dispatch + flags (`flag` pkg) | `flag` |

No third-party modules. `go.mod` has zero `require`d deps.

## CLI

```
ptp <command> [flags]

Commands:
  start            Preflight, run the proxy, and start the Tailscale Funnel
  reset            Stop the Funnel (tailscale funnel reset) and exit
  status           Print Funnel status + the current route map
  list             Print the live hostname→port map and public URLs
  doctor           Check tailscale / Funnel / portless and print fix links

Flags (start):
  --port <n>          Local proxy HTTP port             (default 8443)
  --interval <sec>    Route refresh period in seconds   (default 20)
  --state <path>      routes.json path                  (default ~/.portless/routes.json)
  --funnel-port <n>   Public funnel port 443|8443|10000 (default 443)
  --bg                Run funnel in background (tailscale funnel --bg)
  --fg                Run funnel in foreground (default)
  --no-funnel         Proxy only; print the tailscale command to run manually
  -h, --help          Show help
  -v, --version       Show version
```

- `start` installs SIGINT/SIGTERM handlers that reset the funnel (unless `--bg`)
  and shut the listener down cleanly.
- `--no-funnel` prints the exact `tailscale funnel --bg <port>` command instead
  of running it.

## Error handling

- **Missing/invalid `routes.json`:** treat as empty map, warn once, keep polling
  (portless may not be up yet). Never crash the proxy on a bad read.
- **Backend connection refused** (dev server died between polls): `502` with a
  short plain-text message naming the failed `host:port` (via `ReverseProxy.ErrorHandler`).
- **`tailscale` missing / Funnel disabled / error:** print the stderr plus the
  relevant doc link; exit non-zero on `start` (unless `--no-funnel`).
- **Port already in use:** clear error naming the port and the `--port` flag.

## Distribution

Built once (goreleaser / CI), shipped through five channels:

1. **npm `optionalDependencies` launcher (for `npx`)** — the esbuild/Biome model:
   - Main package `portless-tailscale-proxy`: a tiny **JS `bin` launcher** plus
     `optionalDependencies` on per-platform packages.
   - Per-platform packages, each shipping one prebuilt binary, gated by `os`/`cpu`
     so npm installs only the match:
     `…-darwin-arm64`, `…-darwin-x64`, `…-linux-x64`, `…-linux-arm64`,
     `…-win32-x64`, `…-win32-arm64`.
   - Launcher resolves the platform package's binary (`ptp` / `ptp.exe`) and
     `execFileSync`s it, forwarding argv + stdio. **No postinstall, no install-time
     network** (works in locked-down CI). `npx portless-tailscale-proxy start`
     runs the native binary. WSL resolves to the linux package.
2. **GitHub Releases** — prebuilt archives for every target attached to each tag.
3. **Homebrew tap** — a formula in a tap repo, auto-bumped on release (goreleaser).
4. **`curl | sh` installer** — a hosted script that detects OS/arch and fetches
   the right release binary.
5. **`go install`** — `go install github.com/<owner>/portless-tailscale-proxy@latest`.

Release automation: `goreleaser` cross-compiles the matrix, cuts the GitHub
Release, updates the Homebrew tap, and a publish step pushes the npm launcher +
platform packages (`npm publish --access public`) on tag push using `NPM_TOKEN`
in GitHub Actions.

## Testing

- Unit tests (`go test`) for `loadRoutes` and the routing decision (segment →
  target, strip, miss → 404) against fixture JSON and synthetic requests.
- Integration test: a throwaway backend `httptest.Server`, a route pointed at it,
  asserting path strip, Host rewrite, streamed body passthrough, and a WebSocket
  echo round-trip.
- `doctor`'s external probes (`tailscale`, `portless`) are abstracted behind a
  small runner interface and faked in tests; the real CLIs aren't invoked in CI.

## Out of scope (YAGNI)

- Wildcard-subdomain / host-based routing (Funnel can't do it).
- Auth / access control beyond what Tailscale Funnel already enforces.
- A config file — flags + the portless state file are enough.
- Auto-installing tailscale/portless; `doctor` only links to their installers.
