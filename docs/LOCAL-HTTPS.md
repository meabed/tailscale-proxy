# Local HTTPS names (`tsp run`)

`tsp run` starts an app on a free port and serves it at a stable HTTPS name on this
machine, `https://<name>.localhost`, for as long as it runs. Rules and code style
live in [AGENT.md](../AGENT.md); this page is how the feature works.

```
browser / curl / Bun ──► https://api.acme.localhost ──► tsp proxy (127.0.0.1:443, [::1]:443)
                                                          │  Host → name → port (~/.tailscale-proxy/routes)
                                                          ▼
                                       the app `tsp run` started on 127.0.0.1:<port>
```

It is independent of `tsp start` (the Tailscale Funnel/Serve side). When both run, the
Funnel keeps 443 too (they share it: see [Troubleshooting](TROUBLESHOOTING.md)), and a `tsp run` app is
also discovered by `tsp start` like any other dev server.

## Set up a machine (once)

```bash
tsp service install    # once per machine: the proxy from boot + the CA trusted; then it updates itself
tsp service status     # ✓ boot service · ✓ proxy running · ✓ CA trusted (exit 1 otherwise)
```

## Run an app

```jsonc
// package.json
"scripts": { "dev": "tsp run", "dev:app": "bun --hot src/index.ts" },
"tsp": { "name": "api.acme", "script": "dev:app" }
```

```bash
bun dev                                   # → https://api.acme.localhost
tsp run api.acme -- bun src/index.ts      # any command, no package.json needed
tsp run --force                           # take the name over from another terminal
TSP=0 bun dev                             # run dev:app as is, on its own port
tsp routes                                # running names (--json for scripts)
```

The app gets:

| Variable              | Value                                                                    |
| --------------------- | ------------------------------------------------------------------------ |
| `PORT`                | a port in 4000–4999, the same for a name run after run while it is free  |
| `HOST`                | `127.0.0.1`                                                              |
| `NODE_EXTRA_CA_CERTS` | the local CA, unless already set (Bun/Node calls to other `.localhost` names) |

The app's script must listen on `$PORT` and `$HOST` (`vite --port $PORT --host $HOST`,
`next dev -p $PORT -H $HOST`; Bun and Hono servers read `PORT`). The script runs with the
package manager that ran `tsp run` (`npm_execpath`), so `bun dev` runs `bun run dev:app`.

A name also answers for its subdomains: with `help-chat` running,
`https://acme.help-chat.localhost` reaches it too (the nearest registered parent wins),
for apps that serve one tenant per subdomain.

Requests reach the app with the original `Host` (`api.acme.localhost`) and
`X-Forwarded-Proto: https`, `X-Forwarded-Host`, `X-Forwarded-For`. HTTP/2, streaming
(SSE) and WebSockets (Vite/Next hot reload) pass through.

## How it works

- **Routes** are one JSON file per running app in `~/.tailscale-proxy/routes/<name>.json`
  (`{name, port, pid, dir}`), written atomically by its `tsp run` and removed when it
  exits. A route whose `tsp run` process is gone is ignored and deleted on the next read,
  so a crash or `kill -9` never leaves a name behind. The proxy re-reads the folder only
  when it changes.
- **Certificates**: a local CA in `~/.tailscale-proxy/ca/` (ECDSA P-256, 10 years, key
  `0600`) signs one certificate per exact name on first use (90 days, renewed in memory).
  The CA carries a name constraint for `localhost`: it cannot sign a certificate for any
  other domain that a browser would accept. Names outside `.localhost` get no certificate,
  and the cache is bounded.
- **The service** is installed once per machine (the one sudo) and then updates itself.
  It runs a root-owned copy of `tsp`, the launcher (macOS:
  `/Library/PrivilegedHelperTools/com.meabed.tailscale-proxy`, Linux:
  `/usr/local/lib/tailscale-proxy/tsp`): it binds `127.0.0.1:443` and `[::1]:443` as root,
  switches to your user for good, then execs your own copy, `~/.tailscale-proxy/bin/tsp`,
  with the two sockets inherited (`TSP_LISTEN_FDS`). Root never runs a file you can write:
  the exec happens after the switch. If the copy is missing, older than the launcher, or
  does not answer `--version`, the launcher serves the port itself, so names never stop
  working. macOS: a launchd daemon (`/Library/LaunchDaemons/com.meabed.tailscale-proxy.plist`,
  log `/var/log/tailscale-proxy.log`); Linux: a systemd unit (`tailscale-proxy.service`,
  `journalctl -u tailscale-proxy`). Both restart it within a second when it exits.
- **Updates need no sudo.** Any newer `tsp` you run (brew, npm, `tsp update`) copies itself
  to `~/.tailscale-proxy/bin` (never an older one), and the running proxy, which looks there
  every few seconds, finishes its requests and exits, so the service restarts on the new
  copy. The proxy also checks GitHub for a release once a day (a conditional request,
  jittered), downloads the archive for this platform, checks it against the release's
  `checksums.txt`, and installs it the same way. `tsp configure --auto-update=false`,
  `TSP_NO_UPDATE=1` or `CI` turn that off. In a terminal the CLI says once a day when a
  release is out and how to update it (from a background check: it never waits on the
  network). `tsp service install` asks for sudo again only when the launcher itself
  changes (`TSP_LAUNCHER_REVISION` in the unit), which `tsp doctor` names. `tsp run` in a
  terminal heals a proxy that is down the same way; in CI or a pipe it only warns.
- **Trust**: `tsp service install` ends with it (or `tsp trust` alone). macOS adds the CA
  to your login keychain (one password dialog at the screen; over SSH it is left as a
  next step). Debian/Ubuntu: `/usr/local/share/ca-certificates` +
  `update-ca-certificates`; Fedora/RHEL: `/etc/pki/ca-trust/source/anchors` +
  `update-ca-trust`. Windows: the user's Root store (`certutil -user`).

## Commands

```
tsp run [--force] [<name> -- <cmd...>]   run an app at https://<name>.localhost
tsp routes [--json]                      running names
tsp service install|uninstall|status     the local HTTPS proxy, from boot (install also trusts the CA)
tsp uninstall [--yes] [--all]            remove everything tsp set up (Serve/Funnel entry, service, CA trust, state)
tsp trust                                trust the local CA only
tsp proxy [--port n]                     the proxy in the foreground (what the service runs)
```

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `tsp run` warns "the local HTTPS proxy is not running" | `tsp service install` (it prints the proxy's log when it cannot start) |
| Browser: "not private" / curl: certificate error | `tsp trust` (macOS: at the screen), restart the browser |
| `tsp proxy`: cannot listen on 127.0.0.1:443 | another proxy holds 443: `lsof -nP -iTCP:443 -sTCP:LISTEN` |
| `https://<name>.localhost` → 404 "nothing runs at" | start it: `bun dev` / `tsp run` in its folder; `tsp routes` lists what runs |
| → 502 "not answering" | the app is still starting, or does not listen on `$PORT`/`$HOST` |
| "already runs (pid …)" | stop the other terminal's run, or `tsp run --force` |
| every `*.localhost` hangs | another proxy holds `*:443`: `lsof -nP -iTCP:443 -sTCP:LISTEN` (or move the Funnel: `tsp configure --https-port 8443`) |

Remove everything: `tsp uninstall` (the boot service, the CA's trust, `~/.tailscale-proxy`;
`--all` also config.json). `tsp service uninstall` removes only the boot service.
