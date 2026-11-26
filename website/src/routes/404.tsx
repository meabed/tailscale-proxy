import { createFileRoute } from '@tanstack/react-router';

import { NotFound } from '@/components/not-found';
import { siteName } from '@/lib/pages';

// Prerendered to `404.html` for the static host; client-side misses render the router's `defaultNotFoundComponent`.
export const Route = createFileRoute('/404')({
  head: () => ({ meta: [{ title: `Page not found – ${siteName}` }, { name: 'robots', content: 'noindex' }] }),
  component: NotFound,
});
