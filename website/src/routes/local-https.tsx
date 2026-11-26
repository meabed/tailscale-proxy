import { createFileRoute } from '@tanstack/react-router';

import { pageHead } from '@/lib/pages';

import Content from '../../content/local-https.mdx';

export const Route = createFileRoute('/local-https')({
  head: () => pageHead('/local-https'),
  component: Content,
});
