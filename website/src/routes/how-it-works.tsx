import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/how-it-works.mdx';

export const Route = createFileRoute('/how-it-works')({
  head: () => pageHead('/how-it-works'),
  component: Content,
});
