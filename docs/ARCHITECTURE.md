# Architecture

How tsp works inside, for contributors. What it does and how to use it is on the site,
https://tailscaleproxy.vercel.app (its pages are `website/content/*.mdx`); rules and code style are in
[AGENT.md](../AGENT.md).

tsp is two independent proxies that end at the same dev servers:

| | Online links (`tsp`, `tsp start`) | Local HTTPS names (`tsp run`) |
| --- | --- | --- |
| Address | `https://<node>.<tailnet>.ts.net/<project>/…` | `https://<name>.localhost/…` |
| Who reaches it | anyone (Funnel) or the tailnet (Serve) | this machine |
| TLS ends at | Tailscale | tsp, with its own CA |
| Routes by | first path segment = the project folder of a dev server found by port | Host = a name a `tsp run` registered |
| Runs | in the foreground (or `--bg`) | from boot, as a service |

Everything tsp keeps for a user is in `~/.tsp` (`statedir.go`).

```
~/.tsp/
  config.json        defaults for every command (configure)
  ca/                the local CA: ca.pem, ca-key.pem (0600)
  routes/            one <name>.json per running tsp run, plus its <name>.lock
  bin/               the proxy's own copies of tsp: tsp-<release>, state.json
  proxy.json         which proxy serves now (written by the supervisor)
  update.json        the daily release check
  logs/              proxy.jsonl (events), stats.jsonl (per-minute requests)
  run/<pid>.sock     each running proxy's admin socket (tsp stats)
```

## Online links

`tailscale funnel|serve --bg <port>` sends plain HTTP to tsp's routing proxy on `127.0.0.1:18443` (`cli.go`,
`controller.go` for the desktop app).

1. **Discovery** (`discover*.go`, every `--interval`): listening sockets in the port range with their pid
   (`lsof`/`ps` on macOS and Linux, cached per pid; `netstat`/`tasklist` on Windows); the runtime from the
   executable name; the slug from the nearest project-root folder (`package.json`, `.git`, `go.mod`, …). Within one
   project folder the lowest port gets the clean slug and the others `-<port>`.
2. **Route store** (`store.go`): the slug → service map; a service that disappears stays for `deregisterCycles`
   scans, so a restart does not flap its link.
3. **Routing** (`proxy.go`): the first segment picks the service and is stripped; `Host` becomes
   `localhost:<port>`; `X-Forwarded-*` describe a local request unless `--forward-host`. A `tsp_route` cookie sends a
   page's prefix-less asset and HMR requests to the project the browser last opened. `-` and `_` match alike unless
   `--match-separators=false`. SSE flushes immediately; WebSocket upgrades are relayed.
4. **Exit**: SIGINT/SIGTERM drains the server, then resets the Funnel/Serve entry synchronously.

## Local HTTPS names

- **Routes** (`local_routes.go`): `tsp run` claims `routes/<name>.lock` (a flock held while it runs) and writes
  `<name>.json` atomically. A route whose lock is free is dead: ignored and deleted. The proxy re-reads the folder
  only when it changes.
- **Certificates** (`local_ca.go`): an ECDSA P-256 CA name-constrained to `localhost`; one leaf per exact name on
  first use, cached and renewed in memory. Names outside `.localhost` get no certificate.
- **Trust** (`local_trust.go`): the login keychain on macOS, the system bundle on Debian/Fedora, the user's Root
  store on Windows.

### The boot service and live updates

`tsp service install` (`local_service.go`) installs a launchd daemon or systemd unit (label/unit keep the project's
name, `com.meabed.tailscale-proxy` / `tailscale-proxy.service`) that runs a root-owned copy of tsp, the launcher:

```
launchd/systemd ── tsp proxy --uid --gid --home  (root)
                     │ bind 127.0.0.1:443 and [::1]:443, and :80 when free (http → https)
                     │ drop to the user for good (setgroups, setgid, setuid)
                     ▼
                   supervisor (the user)            holds the sockets, proxy.lock, proxy.json
                     ├─ worker: ~/.tsp/bin/tsp-1.14.0 proxy   inherits the sockets (TSP_LISTEN_FDS, TSP_HTTP_FDS)
                     └─ worker: ~/.tsp/bin/tsp-1.15.0 proxy   during a swap, both accept
```

Root never runs a file the user can write: everything after the bind runs as the user. `launcherRevision` (in the
unit's environment) changes only when the launcher must change; that reinstall is the only update that asks for
sudo.

`supervisor_unix.go`:

- **Workers** get the listening sockets, a ready pipe and a parent pipe as inherited files. A worker proves itself
  before it takes traffic (`selfCheck`: a real HTTPS request to `tsp-health.localhost` through a private listener,
  verified against the CA), writes `ready`, then serves. It stops on SIGTERM or when the parent pipe reaches EOF
  (the supervisor died; Linux also sets `Pdeathsig`).
- **A new release** lands in `bin/` (`proxy_bin.go`: any newer CLI copies itself in, `autoupdate.go` downloads one
  daily and verifies it against the release's `checksums.txt`; `bin/state.json` names the current and previous
  copies and the bad ones). The supervisor polls `bin/state.json`, starts the new worker beside the serving one, and
  only once it is ready tells the old one to drain.
- **Draining** (`localServer.shutdown`): close the listeners but keep serving, wait until no connection is new or
  mid-request, then `http.Server.Shutdown` closes the idle keep-alives. (Calling `Shutdown` first would drop a
  connection accepted a moment before: a shutting-down server closes one whose first request arrives after.)
- **Rollback**: a release that fails readiness or exits within `probation` (60 s) of taking over is marked bad
  (never tried again, `syncProxyBin` and the auto-update skip it) and the previous copy becomes current.
- **Crashes** restart the worker with backoff (1 s doubling to 30 s); three in five minutes set the release aside.
  While no worker serves, the supervisor serves the names itself (`serveBuiltin`), so `*.localhost` never goes dark.
  A copy older than the launcher is never run: the launcher serves instead.
- **Liveness**: the supervisor asks the worker's admin socket `/health` every 30 s; three misses kill it (then the
  crash path).

## Logs and metrics

- **Events** (`applog.go`): `log/slog` JSON lines in `logs/proxy.jsonl`, tagged with role (`supervisor`, `worker`,
  `proxy`), pid and release; 5 MB × 3 files, rotated under `logs/rotate.lock`, each writer reopening the file when
  another rotated it. Requests are logged per `localRequestLog`: failed upstreams, 5xx and slow (> 2 s to first byte)
  by default. `tsp logs` reads them.
- **Metrics** (`metrics.go`): per name, atomic counters (status classes, upstream failures, bytes) and a fixed-bucket
  histogram of time to first byte; open and busy connections from `ConnState`; certificates issued. No lock on the
  request path beyond a `sync.Map` read. Each proxy answers `/stats` and `/health` on `run/<pid>.sock`, and appends a
  per-minute delta to `logs/stats.jsonl` (2 MB × 3; a quiet proxy writes a heartbeat every 10 minutes). The online
  proxy writes the same lines with `"proxy":"start"`. `tsp stats` (`stats_cmd.go`) adds them up over a window.

### What the proxy answers itself

`local_pages.go`: `tsp.localhost` (reserved) is a read-only dashboard and `/api/status`; an unknown name gets a 404
with the running names and a way to run an app there per stack; a registered name whose app does not answer gets a
502 with its port, pid and folder. Browsers (`Accept: text/html`) get HTML via `html/template`, other clients plain
text. Port 80 answers every `http://*.localhost` with a 308 to the same URL over https. `tsp run <name> -- '<one
command>'` runs it with `/bin/sh -c` (`exec` for a simple command, so signals and exit codes pass straight through).

### Finding dev servers for local names

`local_watch.go`: the worker runs the same discovery as `tsp start` (`discover.go`, per-PID cache, ports from the
config plus 4000-4999) into a `routeStore` keyed by local name, every 10s while names get requests and every 60s
when idle, and once (at most once a second, shared by every waiting request) when a request asks for a name it does
not know. Ports held by `tsp run` apps are left out; a `tsp run` route wins on a name clash. `project_config.go`
resolves each project root's config (first of `.tsp.json`, package.json `tsp`, deno.json(c) `tsp`, pyproject.toml
`[tool.tsp]`, Cargo.toml `[package.metadata.tsp]`, composer.json `extra.tsp`), cached by path, mtime and size:
`name`, `path` (Tailscale slug), `port` (the main listener), `share` (`false` leaves `tsp start` out; `"private"`
refuses requests Tailscale marks `Tailscale-Funnel-Request`), `enabled`, `script` (`tsp run`). The CLI reads found
names from the worker's admin socket (`/routes`).
