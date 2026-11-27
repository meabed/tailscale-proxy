import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/logs-and-stats.mdx';

export const Route = createFileRoute('/logs-and-stats')({
  head: () => pageHead('/logs-and-stats'),
  component: Content,
});
