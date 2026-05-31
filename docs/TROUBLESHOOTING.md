# Troubleshooting

Start with `tsp doctor` — it diagnoses the most common setup problems and prints
the exact fix link for each.

## "It works from my phone but not from my Mac" (the MagicDNS gotcha)

**Symptom:** from the same machine running `tsp`, opening
`https://<node>.ts.net/<project>/` behaves oddly or hits a different server than
expected, even though `tsp list` shows the route.

**Why:** from the host, MagicDNS resolves `<node>.ts.net` to your **tailnet IP**
(`100.x`), so the request may be answered locally instead of traversing the public
Funnel relay.

**Fixes:**

- **Test from outside your tailnet** — a phone on cellular, another network, or an
  online "fetch this URL" tool. The public Funnel path works correctly there.
- **Force the public ingress from the host** by bypassing MagicDNS:

  ```bash
  PUBIP=$(dig +short <node>.ts.net @1.1.1.1 | head -1)   # public Funnel ingress IP
  curl -s -i --resolve "<node>.ts.net:443:$PUBIP" "https://<node>.ts.net/<project>/"
  ```

## No services found

`tsp` only registers listeners whose runtime is a known web runtime (`node`, `bun`,
`deno`) within the port range. If `tsp list` is empty:

- Make sure your dev server is actually listening in range (default `3000-5000`).
- Widen the range: `tsp start --ports 3000-9000` (a single port also works:
  `--ports 4000`).
- Include non-web processes: `tsp start --all`, or add runtimes:
  `tsp start --runtimes node,bun,python`.

## `lsof` not found (macOS/Linux)

Discovery uses `lsof`. Install it: `apt install lsof` / `dnf install lsof`. macOS
ships it. `tsp doctor` reports this under "service discovery".

## A page loads without styles / behaves differently than localhost

Fixed by cookie route-affinity: visiting `…/<slug>/` sets a `tsp_route` cookie so
that tab's prefix-less requests (`/_next/...`, `/api/...`, HMR) reach the right
backend. If a page still looks wrong:

- Make sure you opened it via its **`/<slug>/`** URL (that's what sets the cookie),
  not a bare asset URL.
- Don't actively use two different apps in the **same browser** at once — affinity
  is per-browser. Use separate browsers/profiles, or re-visit the `/<slug>/` URL to
  switch which app the tab is pinned to.

## The same project shows on multiple ports

`tsp` collapses every listener of the **same project** into one entry and serves the
**most recent** instance (highest PID, then higher port) — so a restart leftover or
a dev server that binds two ports won't create duplicate routes. The chosen and
dropped ports/pids are shown in `tsp list`/`status` and the startup logs. If the
wrong instance is chosen, stop the stale one (the logs print its pid).

## Wrong project name, or a `-<port>` suffix on a slug

The slug is the nearest project-root folder (the directory containing
`package.json`/`.git`/etc.). Two **distinct** projects that share a folder name get a
`-<port>` suffix so each stays unique — that's expected. Check `tsp list` for the
canonical slugs.

## A service disappeared but the route lingers (or vice-versa)

By design, a service missing from discovery is kept for `--deregister-cycles`
(default 5) scans before its route is removed — this prevents flapping when a dev
server restarts. Lower it (`--deregister-cycles 1`) for immediate removal, or raise
it for longer grace. New services appear on the next scan (`--interval`, default 20s;
lower it for faster pickup).

## Reaching services from a Docker container ("Failed to resolve …ts.net")

Containers don't use your host's MagicDNS, so a tailnet name like
`bigfoot.quoll-adhara.ts.net` won't resolve inside them. Two ways to fix it:

**A. Talk to the `tsp` proxy directly — no MagicDNS, no Tailscale (simplest).**
Bind the proxy to a reachable address, then have the container hit it by path:

```bash
tsp --bind 0.0.0.0            # proxy now listens on all interfaces
```
```bash
# from inside the container — routes by /<slug>/ to your local dev server:
curl http://host.docker.internal:8443/<slug>/        # Docker Desktop (mac/win)
# Linux: start the container with
#   docker run --add-host=host.docker.internal:host-gateway ...
# or use the docker bridge gateway IP (often 172.17.0.1).
```

This routes `container → host tsp → 127.0.0.1:<service>` with no DNS or tailnet
involved. ⚠ `0.0.0.0` exposes the proxy to your LAN — bind a specific interface
(e.g. `--bind 172.17.0.1`, the docker bridge) to narrow it.

**B. Make the tailnet name resolve in the container.**
If the container is on the tailnet (or can route `100.x`), map the name to your
node's tailnet IP so HTTPS certs still match:

```bash
docker run --add-host "bigfoot.quoll-adhara.ts.net:$(tailscale ip -4)" ...
```

## `502` upstream error

The registered dev server isn't accepting connections (crashed or exited between
scans). The body names the failed `127.0.0.1:<port>`. It clears on the next scan once
the server is back (or after the de-register debounce if it stays down).

## Funnel still on after a hard kill

`tsp` resets Serve/Funnel synchronously on `Ctrl-C`. After a `kill -9`, clear it
manually:

```bash
tsp reset                # or: tsp reset --private   (for Serve)
tailscale serve status   # should print "No serve config"
```

## Port already in use

`tsp` listens on `--port` (default `8443`). If something else holds it:
`tsp start --port 9000`.

## Background mode

`tsp start --bg` detaches and logs to `./tsp.log`. Stop it by `kill`-ing the printed
pid (or `pgrep -f "tsp"`), then `tsp reset` to be sure the Serve/Funnel entry is down.
