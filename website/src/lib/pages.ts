// The sidebar: groups in order, each page's title is its sidebar label and its `<title>` (the home page's `<title>` is
// the site's own). A page's body is `content/<path>.mdx`, rendered by the route of the same path; `/` is
// `src/routes/index.tsx`. `summary` is the page's line in llms.txt.
export const pageGroups = [
  {
    title: 'Start',
    pages: [
      { path: '/', title: 'Overview', summary: 'What tsp does, the problems it solves, and a two-minute start.' },
      { path: '/installation', title: 'Install', summary: 'Install tsp, set up local HTTPS once, and connect Tailscale for online URLs.' },
    ],
  },
  {
    title: 'Workflows',
    pages: [
      { path: '/local-https', title: 'Local HTTPS names', summary: 'tsp run: https://<name>.localhost for an app on this machine, with a trusted certificate.' },
      { path: '/share', title: 'Share online', summary: 'tsp: one stable https://<machine>.<tailnet>.ts.net/<project>/ URL per dev server, public (Funnel) or tailnet-only (Serve).' },
      { path: '/webhooks', title: 'Webhooks & OAuth callbacks', summary: 'Point Stripe, GitHub, Slack or an OAuth provider at your laptop once, at a URL that stays the same.' },
      { path: '/agents', title: 'Use with AI agents', summary: 'A prompt and an AGENTS.md section that teach a coding agent to run and share apps with tsp.' },
      { path: '/desktop', title: 'Desktop app', summary: 'The menu-bar app: the same sharing without a terminal.' },
    ],
  },
  {
    title: 'Reference',
    pages: [
      { path: '/commands', title: 'Commands', summary: 'Every command and flag.' },
      { path: '/configuration', title: 'Configuration', summary: 'Saved defaults in ~/.tsp/config.json and the host header.' },
      { path: '/how-it-works', title: 'How it works', summary: 'Discovery, routing, the local proxy, certificates and the boot service.' },
      { path: '/troubleshooting', title: 'Troubleshooting', summary: 'Symptoms and the command that fixes each.' },
    ],
  },
] as const;

type Page = (typeof pageGroups)[number]['pages'][number];

export const pages = pageGroups.flatMap((group): readonly Page[] => group.pages);

export const siteName = 'tailscale-proxy';
export const siteUrl = 'https://tailscaleproxy.vercel.app';

export function pageHead(path: Page['path']) {
  return { meta: [{ title: `${pages.find((page) => page.path === path)?.title} – ${siteName}` }] };
}
