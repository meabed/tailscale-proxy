# tailscale-proxy (`tsp`)

[![ci](https://github.com/meabed/tailscale-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/meabed/tailscale-proxy/actions/workflows/ci.yml)
[![npm](https://img.shields.io/npm/v/tailscale-proxy)](https://www.npmjs.com/package/tailscale-proxy)
[![docs](https://img.shields.io/badge/docs-tailscaleproxy.vercel.app-blue)](https://tailscaleproxy.vercel.app)

Real HTTPS addresses for your local dev servers. On your machine, each app gets a trusted
`https://<name>.localhost`; online, each dev server gets a link through Tailscale that stays the same, for
webhooks, OAuth callbacks, your phone and your teammates.

```
https://api.acme.localhost                          → your app, only from this machine   (found by itself)
https://bigfoot.tail1234.ts.net/api/webhooks/stripe → the same app, from anywhere        (tsp)
```

**Docs: https://tailscaleproxy.vercel.app** · for coding agents: [llms-full.txt](https://tailscaleproxy.vercel.app/llms-full.txt)

## Install

```bash
brew install meabed/tap/tsp        # macOS and Linux
npm i -g tailscale-proxy           # anywhere (the command is tsp)
```

macOS, Linux, Windows and WSL, on amd64 and arm64. There is also a
[desktop app](https://tailscaleproxy.vercel.app/desktop) for the menu bar.

## Use it

```bash
tsp service install     # once per machine: https://*.localhost from boot (your password once)
tsp run                 # in an app folder: serves it at https://<name>.localhost
tsp                     # share every running dev server online (tsp --private: your tailnet only)
tsp doctor              # check everything; each ✗ comes with the command that fixes it
tsp stats               # how the proxies run: health, requests per name, p50/p95
tsp uninstall           # remove everything tsp set up
```

Start your dev servers the way you always do: the local proxy finds each one and serves it at
`https://<folder>.localhost`, with no script changes and nothing for teammates to install. Name a project in
the file its stack already has (first found wins): `.tsp.json`, `"tsp"` in `package.json` / `deno.json`,
`[tool.tsp]` in `pyproject.toml`, `[package.metadata.tsp]` in `Cargo.toml`, `"extra": {"tsp"}` in `composer.json`:

```json
{ "name": "api.acme" }
```

`tsp run` is the other way, for an app that should get its port from tsp. It reads the name and dev script from
the same config, or takes any command:

```jsonc
"scripts": { "dev": "tsp run", "dev:app": "vite --port $PORT --host $HOST" },
"tsp": { "name": "web.acme", "script": "dev:app" }
```

```bash
tsp run api -- 'uvicorn main:app --reload --port $PORT --host $HOST'   # one quoted command: the shell expands $PORT
tsp run api -- node server.js                                          # an app that reads PORT itself
```

`https://tsp.localhost` lists what runs, with requests and latency per name (`/api/status` as JSON) and a Stop
button per app; `tsp stop <name|port>` does the same from a terminal. `http://*.localhost` opens the same pages over
https.

The local HTTPS proxy updates itself: a newer tsp (from brew, npm or its own daily, checksum-verified check)
takes over live once it proves it serves, and one that fails is set aside while the previous one keeps serving.
Everything tsp keeps is in `~/.tsp`.

| Guide | |
| --- | --- |
| [Install](https://tailscaleproxy.vercel.app/installation) | Set up local HTTPS and Tailscale |
| [Local HTTPS names](https://tailscaleproxy.vercel.app/local-https) | `tsp run`, certificates, the boot service |
| [Share online](https://tailscaleproxy.vercel.app/share) | Funnel or Serve, what gets found, what your app sees |
| [Webhooks & OAuth callbacks](https://tailscaleproxy.vercel.app/webhooks) | Stripe, GitHub, Slack, sign-in redirects |
| [Use with AI agents](https://tailscaleproxy.vercel.app/agents) | A prompt and an AGENTS.md section |
| [Commands](https://tailscaleproxy.vercel.app/commands) | Every command and flag |
| [Troubleshooting](https://tailscaleproxy.vercel.app/troubleshooting) | Symptoms and the fix for each |

## Develop

Go, standard library only. `go test -race ./...`, `go vet ./...`; the site is `website/` (`bun run build`).
[AGENT.md](AGENT.md) has the rules and repo map, [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) how it works
inside, [docs/TESTING.md](docs/TESTING.md) and [docs/RELEASING.md](docs/RELEASING.md) the rest.

## License

MIT
