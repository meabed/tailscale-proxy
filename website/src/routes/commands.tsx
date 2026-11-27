import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/commands.mdx';

export const Route = createFileRoute('/commands')({
  head: () => pageHead('/commands'),
  component: Content,
});
