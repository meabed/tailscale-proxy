import mdx from '@mdx-js/rollup';
import rehypeShiki from '@shikijs/rehype';
import tailwindcss from '@tailwindcss/vite';
import { tanstackStart } from '@tanstack/react-start/plugin/vite';
import { remarkMermaid } from '@theguild/remark-mermaid';
import viteReact from '@vitejs/plugin-react';
import rehypeSlug from 'rehype-slug';
import remarkGfm from 'remark-gfm';
import { defineConfig } from 'vite';

export default defineConfig({
  build: {
    reportCompressedSize: false,
    target: 'es2022',
  },

  // Why: the remark plugin imports the Mermaid component into compiled MDX, which Vite's dependency scan never reads;
  // found late, it is bundled with a second React copy and the first `vite dev` load fails.
  optimizeDeps: {
    include: ['@theguild/remark-mermaid/mermaid'],
  },

  resolve: {
    tsconfigPaths: true,
    dedupe: ['react', 'react-dom'],
  },

  plugins: [
    // Why: MDX compiles to JSX before the React plugin and TanStack Start see the module.
    {
      enforce: 'pre',
      ...mdx({
        providerImportSource: '@mdx-js/react',
        remarkPlugins: [remarkGfm, remarkMermaid],
        rehypePlugins: [rehypeSlug, [rehypeShiki, { themes: { light: 'github-light', dark: 'github-dark' }, defaultLanguage: 'text', lazy: true }]],
      }),
    },
    // Every route is static: prerender each page to `<path>.html`, which Vercel's `cleanUrls` serves at `<path>`
    // (`/404` becomes the `404.html` Vercel answers for any other path).
    tanstackStart({
      prerender: { enabled: true, autoSubfolderIndex: false, crawlLinks: true, failOnError: true },
    }),
    tailwindcss(),
    viteReact({ include: /\.(mdx|tsx?)$/ }),
  ],
});
