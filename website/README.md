# tailscale-proxy docs site

[TanStack Start](https://tanstack.com/start) documentation site for **tailscale-proxy**, prerendered to static
HTML and deployed on **Vercel**. Bun, Vite, MDX, Tailwind v4, oxlint + oxfmt.

## Local development

```bash
cd website
bun install
bun run dev            # http://localhost:5173 (search needs the index that `build` writes)
bun run build          # prerender every page to dist/client + Pagefind search index
bun run preview        # serve the build (http://localhost:4173)
bun run lint           # oxlint --type-aware
bun run format         # bun fmt (.oxfmtrc.json); format:check only reports
bun run typecheck      # bun check (TypeScript 7)
```

## Layout

| Path               | What                                                                                                                                               |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `content/*.mdx`    | Page bodies (`index.mdx` is `/`). `<Callout type="info\|warning">`, `<Tabs items={[…]}><Tabs.Tab>` and ` ```mermaid ` blocks work without imports. |
| `src/lib/pages.ts` | Sidebar order and page titles (the `<title>` is `<title> – tailscale-proxy`).                                                                      |
| `src/routes/*.tsx` | One route per page, rendering its `content/*.mdx`; `__root.tsx` is the shell (header, sidebar, footer).                                            |
| `src/components/`  | Header, sidebar, search (Pagefind), theme toggle, and the MDX components.                                                                          |
| `src/styles.css`   | Tailwind entry; tokens in `src/styles/theme.css`, MDX body styles in `src/styles/prose.css`.                                                       |
| `public/`          | Static assets served at `/`.                                                                                                                       |

**Add a page:** write `content/<name>.mdx`, add `{ path: '/<name>', title }` to `src/lib/pages.ts`, and copy any
`src/routes/<page>.tsx` to `src/routes/<name>.tsx` with the new path. The build prerenders it and indexes it for search.

## Deploy to Vercel

The project's **Root Directory** is `website`; `vercel.json` sets everything else: `bun run build`, output
`dist/client` as a plain static site (no framework preset), clean URLs (`/installation` serves `installation.html`,
`404.html` answers unknown paths). Pushes to `master` redeploy.
