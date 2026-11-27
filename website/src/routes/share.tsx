import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/share.mdx';

export const Route = createFileRoute('/share')({
  head: () => pageHead('/share'),
  component: Content,
});
