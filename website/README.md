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

| Path                     | What                                                                                                                                                            |
| ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `src/routes/index.tsx`   | The home page: the two addresses taken apart, the problems each solves, the two-lane map, the quick start, the agent prompt.                                    |
| `content/*.mdx`          | Every other page's body. `<Address>`, `<ReachMap>`, `<AgentPrompt>`, `<Callout>`, `<Tabs>` and ` ```mermaid ` blocks work without imports.                      |
| `src/lib/pages.ts`       | Sidebar groups, page titles (the `<title>` is `<title> – tailscale-proxy`) and each page's llms.txt line.                                                       |
| `src/lib/agent-guide.ts` | The agent prompt and AGENTS.md section the copy buttons copy, and the llms.txt intro.                                                                           |
| `scripts/gen-llms.ts`    | Writes `public/llms.txt` and `public/llms-full.txt` (every page as Markdown) before each build and dev start; both are git-ignored.                             |
| `src/components/`        | Header, sidebar (and its phone menu), search (Pagefind), theme toggle, and the MDX components.                                                                  |
| `src/styles.css`         | Tailwind entry; tokens in `src/styles/theme.css` (green `--local` and blue `--accent` say who can reach an address), MDX body styles in `src/styles/prose.css`. |
| `public/`                | Static assets served at `/`.                                                                                                                                    |

**Add a page:** write `content/<name>.mdx`, add `{ path: '/<name>', title, summary }` to a group in `src/lib/pages.ts`,
and copy any `src/routes/<page>.tsx` to `src/routes/<name>.tsx` with the new path. The build prerenders it, indexes it
for search and adds it to llms.txt.

## Deploy to Vercel

The project's **Root Directory** is `website`; `vercel.json` sets everything else: `bun run build`, output
`dist/client` as a plain static site (no framework preset), clean URLs (`/installation` serves `installation.html`,
`404.html` answers unknown paths) and redirects the old `/getting-started` and `/usage` to `/share` and `/commands`.
Pushes to `master` redeploy.
