import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/webhooks.mdx';

export const Route = createFileRoute('/webhooks')({
  head: () => pageHead('/webhooks'),
  component: Content,
});
