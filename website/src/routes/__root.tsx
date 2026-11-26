import { MDXProvider } from '@mdx-js/react';
import { createRootRoute, HeadContent, Outlet, Scripts, useLocation } from '@tanstack/react-router';

import { Callout } from '@/components/callout';
import { CodeBlock } from '@/components/code-block';
import { SiteFooter } from '@/components/site-footer';
import { SiteHeader } from '@/components/site-header';
import { SiteSidebar } from '@/components/site-sidebar';
import { Tabs } from '@/components/tabs';
import { themeScript } from '@/components/theme-toggle';
import { pages, siteName } from '@/lib/pages';

import appCss from '../styles.css?url';

// What the MDX pages render with: `<Callout>` and `<Tabs>` are used without imports; `pre` adds the copy button.
// `<Mermaid>` is imported by the remark plugin into each page that has a ```mermaid block.
const mdxComponents = { Callout, Tabs, pre: CodeBlock };

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: 'utf-8' },
      { name: 'viewport', content: 'width=device-width, initial-scale=1' },
      { title: `${siteName} — self-hosted ngrok alternative on Tailscale` },
      {
        name: 'description',
        content: 'Discover local dev servers by port and expose them through one Tailscale Serve/Funnel entry, routed by project name. An open-source, self-hosted ngrok alternative.',
      },
    ],
    links: [
      { rel: 'stylesheet', href: appCss },
      { rel: 'icon', type: 'image/svg+xml', href: '/app-icon.svg' },
    ],
    scripts: [{ children: themeScript }],
  }),
  shellComponent: RootDocument,
});

function RootDocument() {
  // Only the docs pages go into the search index (Pagefind indexes pages that carry data-pagefind-body).
  const isDocsPage = useLocation({ select: ({ pathname }) => pages.some((page) => page.path === pathname) });

  return (
    <html lang='en' dir='ltr' suppressHydrationWarning>
      <head>
        <HeadContent />
      </head>
      <body>
        <SiteHeader />
        <div className='mx-auto flex max-w-(--shell) items-start gap-12 px-4 sm:px-7'>
          <aside className='sticky top-(--header-h) h-[calc(100dvh-var(--header-h))] w-(--sidebar) shrink-0 self-start overflow-y-auto py-7 max-[860px]:hidden'>
            <SiteSidebar />
          </aside>
          <main className='min-w-0 flex-1 pt-12 pb-16 max-[860px]:pt-7 max-[860px]:pb-12'>
            <article className='tsp-content' data-pagefind-body={isDocsPage || undefined}>
              <MDXProvider components={mdxComponents}>
                <Outlet />
              </MDXProvider>
            </article>
            <SiteFooter />
          </main>
        </div>
        <Scripts />
      </body>
    </html>
  );
}
