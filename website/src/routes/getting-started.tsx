import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/getting-started.mdx';

export const Route = createFileRoute('/getting-started')({
  head: () => pageHead('/getting-started'),
  component: Content,
});
