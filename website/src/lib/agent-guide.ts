// What the site gives coding agents, in one place: the prompt the copy buttons copy, the section a repo keeps in its
// AGENTS.md, and the llms.txt intro (`scripts/gen-llms.ts` writes /llms.txt and /llms-full.txt from these and the pages).
import { siteUrl } from './pages';

export const llmsIntro = `tailscale-proxy (command: tsp) gives local dev servers real HTTPS addresses. On this machine: https://<name>.localhost for an app started with \`tsp run\`, with a certificate the OS trusts. Online: one stable https://<machine>.<tailnet>.ts.net/<project>/ URL per running dev server through Tailscale Funnel (public) or Serve (tailnet only), for webhooks, OAuth callbacks, phones and teammates. Open source, one Go binary, macOS, Linux, Windows.`;

export const agentPrompt = `You are working in a project whose dev servers run through tailscale-proxy (tsp). Use it instead of raw ports.

Check the machine first, and fix what is missing:
- \`tsp --version\` (missing: \`brew install meabed/tap/tsp\` or \`npm i -g tailscale-proxy\`)
- \`tsp doctor\` checks both halves; each ✗ is followed by the command that fixes it, and it exits 1 when one fails.
- \`tsp service install\` fixes local HTTPS; it may ask the user for their password once, so tell them.
- Tailscale sign-in and Funnel are the user's to do in a browser; give them the link doctor prints.

Run an app at a local HTTPS name:
- Start the dev server the usual way (npm run dev, uvicorn, rails s, go run .): tsp finds it and serves it at https://<project folder>.localhost. Never hard-code a port.
- To pick the name, put it in the project: .tsp.json { "name": "<app>.<project>" }, or the "tsp" key of package.json / deno.json, [tool.tsp] in pyproject.toml, [package.metadata.tsp] in Cargo.toml, "extra"."tsp" in composer.json.
- \`tsp routes --json\` lists the names that run (source "found" or "run"); https://tsp.localhost/api/status has them with requests and latency.
- A port is taken by a dev server left running: \`tsp stop <port>\` or \`tsp stop <name>\`, with --yes (there is no terminal to ask in) and --force when it ignores SIGTERM. Stop only servers you started, unless the user says so.
- To run a command through tsp instead (it picks a free port and sets $PORT and $HOST): \`tsp run <name> -- '<command using $PORT and $HOST>'\`, e.g. \`tsp run api.acme -- 'uvicorn main:app --port $PORT --host $HOST'\`.

Share an app online (webhooks, OAuth callbacks, a phone, a teammate):
- \`tsp list\` shows each running dev server and its URL, https://<machine>.<tailnet>.ts.net/<project>/.
- \`tsp\` (public, Funnel) or \`tsp --private\` (tailnet only, Serve) keeps those URLs up while it runs; \`tsp --bg\` runs it detached.
- The URL is the same every time on this machine, so register it once with Stripe, GitHub, Slack or an OAuth provider.
- Apps see Host: localhost by default; use \`tsp --forward-host\` when the app builds absolute URLs (OAuth redirects).

Report the URLs you used. Docs for agents: ${siteUrl}/llms-full.txt`;

export const agentsMdSection = `## Local URLs (tailscale-proxy)

- Start apps as usual: tsp finds every dev server and serves it at https://<name>.localhost, named by the project's .tsp.json or "tsp" config (default: its folder). Never hard-code ports. https://tsp.localhost shows what runs.
- First run on a machine: \`tsp service install\` (local HTTPS proxy from boot + trusted CA). \`tsp service status\` checks it.
- Running names: \`tsp routes --json\`; stop one: \`tsp stop <name|port> --yes\`. Online URLs for webhooks and callbacks: \`tsp list\`, then \`tsp\` (public) or \`tsp --private\` (tailnet).
- Docs: ${siteUrl}/llms-full.txt`;
