// Sidebar order. A page's title is its sidebar label and its `<title>` (the home page's `<title>` is the site name);
// its body is `content/<path>.mdx` (`/` is `index.mdx`), rendered by the route of the same path.
export const pages = [
  { path: '/', title: 'Introduction' },
  { path: '/installation', title: 'Installation' },
  { path: '/getting-started', title: 'Getting started' },
  { path: '/desktop', title: 'Desktop app' },
  { path: '/usage', title: 'Usage & commands' },
  { path: '/configuration', title: 'Configuration' },
  { path: '/local-https', title: 'Local HTTPS names' },
  { path: '/how-it-works', title: 'How it works' },
  { path: '/troubleshooting', title: 'Troubleshooting' },
] as const;

export const siteName = 'tailscale-proxy';

export function pageHead(path: (typeof pages)[number]['path']) {
  return { meta: [{ title: `${pages.find((page) => page.path === path)?.title} – ${siteName}` }] };
}
