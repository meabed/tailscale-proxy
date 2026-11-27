# AGENT.md

Single source of truth for agents and contributors. **Rules, code style, patterns,
and workflow live here.** User docs live on the site (`website/content/*.mdx`), internals
in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — don't inline them here; keep them updated
as features change.

## What this is

`tailscale-proxy` (binary **`tsp`**) — a single-binary Go CLI that discovers local
dev servers by listening **port** and exposes them through **one** Tailscale
Serve/Funnel entry, routed by URL path:

```
https://<node>.ts.net/<project>/foo   →   strip segment → 127.0.0.1:<port>/foo
```

It also serves apps locally at stable HTTPS names (`https://<name>.localhost` behind
`tsp proxy` on 443 with its own CA): every dev server the proxy finds by itself (the same
discovery, named by the project's config or its folder), and apps started with `tsp run`;
from a boot service that updates itself live.

Self-hosted ngrok alternative. Go **stdlib only**, zero runtime dependencies,
cross-platform (macOS, Linux, Windows, WSL). Module `github.com/meabed/tailscale-proxy`.

## Spec & reference docs (keep updated; link, don't duplicate)

| Topic | File |
| --- | --- |
| User docs (the one source: install, guides, commands, troubleshooting) | `website/content/*.mdx` → https://tailscaleproxy.vercel.app · [website/README.md](website/README.md) |
| Internals: both proxies, the supervisor and live updates, logs and metrics | [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) |
| Testing approach & helpers | [docs/TESTING.md](docs/TESTING.md) |
| Release process | [docs/RELEASING.md](docs/RELEASING.md) |
| Entry point (short; links to the site) | [README.md](README.md) |

When a flag or behavior changes, update the site page that describes it, the CLI's
help, and ARCHITECTURE.md when internals change. The README stays short.

## Repo map

The engine + CLI live in **`core/`** (`package core`, importable). Root `main.go`
is a thin `package main` shell (`core.Run`). The desktop app is a **separate Go
module** under `desktop/` that imports `core` and drives it in-process.

| File | Responsibility |
| --- | --- |
| `main.go` (root) | Thin entry — `os.Exit(core.Run(os.Args[1:]))` |
| `core/dispatch.go` | Subcommand dispatch (bare `tsp` / leading flag → `start`); `Version` |
| `core/cli.go` | `start`: flags, server lifecycle, signal handling, header |
| `core/commands.go` | `list` / `status` / `reset` / `configure` + `queryConfig` |
| `core/controller.go` | **`Controller`** (Start/Stop/Status/OnChange), `Options`/`OptionsFromConfig`, `CheckUpdate`, `CheckTailscale` — the embedder API; export only what `desktop/` uses |
| `core/config.go` | `Config`, `defaultConfig()` overlay, `LoadConfig`/`SaveConfig`/`ConfigPath` (`~/.tsp/config.json`), `writeFileAtomic` (every state file) |
| `core/statedir.go` | `stateDir`: `~/.tsp`, everything tsp keeps for a user |
| `core/discover.go` | `Service`/`Duplicate` model, runtime classification, slug from project root, `buildServices` |
| `core/discover_unix.go` | `//go:build !windows` — `lsof`/`ps` listeners + parsers |
| `core/discover_windows.go` | `//go:build windows` — `netstat`/`tasklist` + parsers |
| `core/store.go` | `routeStore`: `refresh`, debounced de-registration |
| `core/proxy.go` | `newHandler`: reverse proxy, path routing, cookie affinity, Host rewrite |
| `core/expose.go` | `Runner` iface, `Mode` (Funnel/Serve), `tailscale serve\|funnel\|set`, `nodeDNSName`, accept-dns |
| `core/doctor.go` | `runDoctor` + `check{}` + `printChecks`; `tsp doctor` adds `localHTTPSChecks` (shared with `tsp service status`) |
| `core/output.go` | Start header, service URLs, duplicate notes |
| `core/poll.go` | Periodic re-scan loop + logging (CLI) |
| `core/update.go` | Self-update: standalone replaces itself; brew/npm run their upgrade (npm first: Homebrew's Node keeps globals under `/opt/homebrew/lib/node_modules`) |
| `core/local_routes.go` | `localRoute`, name rules, the per-route file registry (`~/.tsp/routes`), stable port pick |
| `core/local_ca.go` | Local CA (name-constrained to `localhost`), per-name leaf issuer with a bounded cache, `caTrusted` |
| `core/local_proxy.go` | `tsp proxy`: `localServer` (HTTPS by Host, metrics, `selfCheck`, graceful `shutdown`), its three modes (supervisor, worker, standalone), `proxy.json` |
| `core/supervisor_unix.go` | The boot service after the privilege drop: holds the 443 sockets, runs `~/.tsp/bin` workers, live swap on readiness, rollback, crash backoff, built-in fallback, liveness |
| `core/metrics.go` | Per-name atomic counters + latency histogram, `meteredWriter`, connection states, the admin socket (`run/<pid>.sock`), per-minute history (`logs/stats.jsonl`) |
| `core/applog.go` | slog JSON event log (`logs/proxy.jsonl`) with a multi-process rotating writer |
| `core/stats_cmd.go` | `tsp stats` and `tsp logs` |
| `core/local_run.go` | `tsp run`: the project config or an explicit command (one quoted command runs in the shell), env, registration, signal pass-through |
| `core/project_config.go` | The project config resolver (`.tsp.json`, package.json, deno.json(c), pyproject.toml, Cargo.toml, composer.json), cached per file; minimal TOML table reader |
| `core/local_watch.go` | The local proxy's watcher: discovery into an in-memory store keyed by name, adaptive interval, on-demand rescan for unknown names |
| `core/local_pages.go` | What the local proxy answers itself: the `tsp.localhost` dashboard, `/api/status` and the stop API, 404/502 pages (HTML or text), port 80 (same pages over https) |
| `core/local_options.go` | What the local proxy answers besides served names, live from config.json: `localDashboard` (on), `localCatchAll` (off: unknown names get no certificate, port 80 closes) |
| `core/stop.go` | `tsp stop <name\|port\|--pid>` and the dashboard's Stop: verify, signal, wait, per-OS commands (`process_*.go` signals) |
| `core/local_service.go` | `tsp service install/uninstall/status` (launchd/systemd), sudo re-run, test seams |
| `core/proxy_bin.go` | The proxy's copies in `bin/` (`tsp-<release>`, `state.json`: current, previous, bad), `syncProxyBin` (newer CLIs copy themselves in), `markBad`, `versionLess` |
| `core/autoupdate.go` | Daily release check (ETag) + checksummed install into `bin/` (skips bad releases); `update.json`; the CLI's once-a-day notice |
| `core/uninstall.go` | `tsp uninstall`: Serve/Funnel entry (only tsp's), service, CA trust, state dir, how to remove the CLI |
| `core/local_trust.go` | `tsp trust`: per-OS trust-store commands |
| `core/service_*.go` / `core/process_*.go` | Build-tagged init-system paths; pid liveness, stop, privilege drop |
| `core/detach.go` (+ `detach_unix.go` / `detach_windows.go`) | `--bg` background spawn; the build-tagged files hold only `detachSysProcAttr` |
| `desktop/` | **Separate module** — Wails v3 tray app over `core.Controller`. See [desktop/README.md](desktop/README.md) |

Tests are `*_test.go` beside each file in `core/`. The test binary doubles as tsp
(`TSP_TEST_AS_TSP=1`, `supervisor_unix_test.go`), so the supervisor tests run real workers.
See [docs/TESTING.md](docs/TESTING.md).

## Code style

- **`core` is stdlib-only.** No third-party runtime dependencies in the engine/CLI
  — keep it that way. (The `desktop/` module is separate and may use Wails.)
- One clear responsibility per file; split when a file grows.
- Concise doc comments on exported identifiers; match the surrounding density. No
  noisy inline narration.
- `gofmt`-clean and `go vet`-clean before committing.
- Wrap errors with context (`fmt.Errorf("…: %v", err)`); surface `stderr` from
  external commands in the message.

## Patterns (follow these)

- **External commands go through the `Runner` interface** (`expose.go`). Real impl
  is `execRunner`; tests inject `fakeRunner` / `scriptRunner`. Don't call
  `exec.Command` for `tailscale`/`lsof` outside Runner-backed helpers.
- **Platform differences use build tags** (`//go:build !windows` / `windows`), not
  runtime `runtime.GOOS` checks — for discovery and detach.
- **Config:** always start from `defaultConfig()` then overlay the file; zero values
  are **not** defaults. Per-run flags override config.
- **Doctor `Check`:** `Fix` prints only when the check fails; `Note` is advisory and
  prints regardless. Advisories (e.g. the MagicDNS note) keep `OK: true` so they
  never fail `doctor`.
- **Modes:** `ModeFunnel` (public) vs `ModeServe` (`--private`).
- **Proxy:** first path segment = slug, stripped before forwarding; dial
  `127.0.0.1:<port>`; rewrite `Host: localhost:<port>`; cookie route-affinity
  (`tsp_route`) routes prefix-less asset/HMR requests; SSE streamed, WebSocket
  upgrades proxied.
- **Slug matching:** slugs are canonically dash-separated (`slugify`). With
  `--match-separators` (default on), `routeStore.lookup` retries an exact miss
  with `_`→`-`, so `/module_api/` reaches the `module-api` route. Off = exact-dash.
- **De-registration is debounced** by `deregisterCycles` so brief restarts don't
  flap routes.
- Default presents a **local** request to apps (`X-Forwarded-*`, not PROXY
  protocol); `--forward-host` is opt-in for apps needing the public host.

## Conventions (behavior rules established across sessions)

- **Local HTTPS names:** a `tsp run` route is one file per running run, removed on exit and
  ignored once its pid is gone; never a shared mutable file. Found routes live only in the
  worker's memory; a `tsp run` route wins over a found one of the same name. One project
  config resolver serves both sides; an invalid file leaves its project out and is reported
  (`tsp routes`, doctor, dashboard), never guessed around. The CA only ever signs
  `localhost` names (name constraint + issuer check). The service binary is a root-owned
  launcher: it binds 443, drops to the user for good and supervises workers from
  `~/.tsp/bin` (root never runs a user-writable file). A new release serves only after its
  readiness check; one that fails or dies within probation is marked bad and the previous
  copy serves. Never `Shutdown` a server before closing its listeners and letting busy
  connections finish (`localServer.shutdown`). Bump `launcherRevision` only when the
  launcher itself changes (that reinstall asks sudo).
  Commands that need the terminal (sudo, the macOS trust dialog) go through `interactive`;
  tests pin `geteuid`/`interactive`/`serviceRunner` and never touch the real service paths.

- **System/global mutations are opt-in, default off, never silently reverted, and
  log how to undo.** Examples: `--accept-dns` (default unset = leave Tailscale DNS
  alone), `--bind` (default `127.0.0.1`; warn when binding beyond loopback).
- Validate user-facing flag values; reject bad input with **exit code 2** and a
  clear message.
- Reset the Serve/Funnel entry **synchronously** on exit (Ctrl-C); start listening
  before exposing.
- Prefer the smallest accurate change; no legacy/back-compat shims unless asked.

## Dev workflow

- **Go** is installed via Homebrew. If `go` is stale:
  `export GOROOT=/opt/homebrew/opt/go/libexec; export PATH="$GOROOT/bin:$PATH"`.
- Format / lint: `gofmt -w .` · `go vet ./...`  (or `bun run format` / `bun run lint`).
- Test: `go test -count=1 ./...` (CI uses `-race`). Always cross-check Windows:
  `GOOS=windows GOARCH=amd64 go build -o /dev/null .`.
- Build: `go build -o tsp .` · all release targets (snapshot): `bun run build:binaries`.
- Desktop app (separate module, needs CGO + system webview):
  `cd desktop && go build -o tsp-app .` (package with `wails3 build`). See
  [desktop/README.md](desktop/README.md).
- Website (`website/`, TanStack Start + MDX, prerendered to static HTML for Vercel): `cd website && bun run build`
  (all pages + Pagefind index), then `bun run lint` · `bun run format:check` · `bun run typecheck`. Pages are
  `website/content/*.mdx`; sidebar order and titles are `website/src/lib/pages.ts`. See
  [website/README.md](website/README.md).
- **Branching:** work on `master` (the default branch). Pushes to `master`/`main`
  trigger semantic-release. Branch first if asked to commit on the default branch;
  push/commit only when the user asks.
- **Conventional commits drive releases:** `feat:` → minor, `fix:` → patch,
  `docs:` / `chore:` / `test:` → no release. See [docs/RELEASING.md](docs/RELEASING.md).
