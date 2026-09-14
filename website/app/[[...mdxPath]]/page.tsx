import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { Mermaid } from "@theguild/remark-mermaid/mermaid";
import { notFound } from "next/navigation";
import { compileMdx } from "nextra/compile";
import { Callout, Tabs } from "nextra/components";
import { evaluate } from "nextra/evaluate";
import { cache } from "react";
import { useMDXComponents as getMDXComponents } from "../../mdx-components";

type PageProps = {
  params: Promise<{ mdxPath?: string[] }>;
};

const contentDirectory = join(process.cwd(), "content");
const components = getMDXComponents({ Callout, Tabs, Mermaid });

const loadPage = cache(async (route: string) => {
  const filename = `${route || "index"}.mdx`;
  const files = await readdir(contentDirectory);
  if (route === "index" || !files.includes(filename)) {
    notFound();
  }
  const filePath = join(contentDirectory, filename);
  const source = await readFile(filePath, "utf8");
  const compiled = await compileMdx(source, { filePath, defaultShowCopyCode: true });
  return evaluate(compiled, components);
});

export const dynamicParams = false;

export async function generateStaticParams() {
  const files = await readdir(contentDirectory);
  return files
    .filter((filename) => filename.endsWith(".mdx"))
    .map((filename) => ({ mdxPath: filename === "index.mdx" ? [] : [filename.slice(0, -4)] }));
}

export async function generateMetadata(props: PageProps) {
  const params = await props.params;
  if (!params.mdxPath?.length) {
    return {
      title: { absolute: "tailscale-proxy" },
      description:
        "Discover local dev servers by port and expose them through one Tailscale Serve/Funnel entry — an open-source, self-hosted ngrok alternative.",
    };
  }
  const { metadata } = await loadPage(params.mdxPath.join("/"));
  return metadata;
}

export default async function Page(props: PageProps) {
  const params = await props.params;
  const { default: MDXContent } = await loadPage(params.mdxPath?.join("/") ?? "");
  return <MDXContent {...props} params={params} />;
}
