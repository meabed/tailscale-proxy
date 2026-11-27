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
- In package.json: "scripts": { "dev": "tsp run", "dev:app": "<the real dev command>" } and "tsp": { "name": "<app>.<project>", "script": "dev:app" }
- The real dev command must listen on $PORT and $HOST (tsp sets both); never hard-code a port.
- \`bun dev\` / \`npm run dev\` then serves https://<app>.<project>.localhost. \`tsp routes --json\` lists what runs.
- Without package.json: \`tsp run <name> -- <command...>\`.

Share an app online (webhooks, OAuth callbacks, a phone, a teammate):
- \`tsp list\` shows each running dev server and its URL, https://<machine>.<tailnet>.ts.net/<project>/.
- \`tsp\` (public, Funnel) or \`tsp --private\` (tailnet only, Serve) keeps those URLs up while it runs; \`tsp --bg\` runs it detached.
- The URL is the same every time on this machine, so register it once with Stripe, GitHub, Slack or an OAuth provider.
- Apps see Host: localhost by default; use \`tsp --forward-host\` when the app builds absolute URLs (OAuth redirects).

Report the URLs you used. Docs for agents: ${siteUrl}/llms-full.txt`;

export const agentsMdSection = `## Local URLs (tailscale-proxy)

- Start apps with \`bun dev\` (or \`npm run dev\`): \`tsp run\` serves each at https://<name>.localhost from package.json "tsp" {name, script}. Never hard-code ports; scripts listen on $PORT and $HOST.
- First run on a machine: \`tsp service install\` (local HTTPS proxy from boot + trusted CA). \`tsp service status\` checks it.
- Running names: \`tsp routes --json\`. Online URLs for webhooks and callbacks: \`tsp list\`, then \`tsp\` (public) or \`tsp --private\` (tailnet).
- Docs: ${siteUrl}/llms-full.txt`;
