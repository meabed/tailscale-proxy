import { createFileRoute } from '@tanstack/react-router';

import { siteName } from '@/lib/pages';

import Content from '../../content/index.mdx';

export const Route = createFileRoute('/')({
  head: () => ({
    meta: [{ title: siteName }, { name: 'description', content: 'Discover local dev servers by port and expose them through one Tailscale Serve/Funnel entry — an open-source, self-hosted ngrok alternative.' }],
  }),
  component: Content,
});
