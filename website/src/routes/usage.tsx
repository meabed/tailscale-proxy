import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/usage.mdx';

export const Route = createFileRoute('/usage')({
  head: () => pageHead('/usage'),
  component: Content,
});
