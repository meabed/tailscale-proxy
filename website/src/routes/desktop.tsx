import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/desktop.mdx';

export const Route = createFileRoute('/desktop')({
  head: () => pageHead('/desktop'),
  component: Content,
});
