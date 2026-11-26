import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/configuration.mdx';

export const Route = createFileRoute('/configuration')({
  head: () => pageHead('/configuration'),
  component: Content,
});
