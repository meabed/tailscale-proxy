# tailscale-proxy docs site

[Nextra](https://nextra.site) (Next.js app router) documentation site for
**tailscale-proxy**, deployed on **Vercel**. Content lives in `content/*.mdx`.

## Local development

```bash
cd website
bun install
bun run dev            # http://localhost:3000
bun run typecheck
bun run build          # production build (+ pagefind search index)
```

Type checks use TypeScript 7's `tsc`, including during `next build`. Bun applies
the patch in `patches/`, which defers Twoslash initialization until a code block
requests it. This avoids [Nextra's startup error](https://github.com/shuding/nextra/issues/5046).

## Deploy to Vercel

1. Import `meabed/tailscale-proxy` into Vercel (New Project).
2. Set **Root Directory** to `website`.
3. Framework preset: **Next.js** (auto-detected). Build command `next build`,
   output handled automatically.
4. Deploy. Pushes to `master` redeploy automatically.

> Edit a page: change the matching file in `content/` and the nav in
> `content/_meta.ts`.
