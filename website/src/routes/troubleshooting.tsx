import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/troubleshooting.mdx';

export const Route = createFileRoute('/troubleshooting')({
  head: () => pageHead('/troubleshooting'),
  component: Content,
});
