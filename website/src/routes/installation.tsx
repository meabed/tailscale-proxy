import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/installation.mdx';

export const Route = createFileRoute('/installation')({
  head: () => pageHead('/installation'),
  component: Content,
});
