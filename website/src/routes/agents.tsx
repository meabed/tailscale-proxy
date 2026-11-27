import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/agents.mdx';

export const Route = createFileRoute('/agents')({
  head: () => pageHead('/agents'),
  component: Content,
});
