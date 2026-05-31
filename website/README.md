# tailscale-proxy docs site

[Nextra](https://nextra.site) (Next.js app router) documentation site for
**tailscale-proxy**, deployed on **Vercel**. Content lives in `content/*.mdx`.

## Local development

```bash
cd website
npm install            # or: bun install / pnpm install
npm run dev            # http://localhost:3000
npm run build          # production build (+ pagefind search index)
```

## Deploy to Vercel

1. Import `meabed/tailscale-proxy` into Vercel (New Project).
2. Set **Root Directory** to `website`.
3. Framework preset: **Next.js** (auto-detected). Build command `next build`,
   output handled automatically.
4. Deploy. Pushes to `master` redeploy automatically.

> Edit a page: change the matching file in `content/` and the nav in
> `content/_meta.ts`.
