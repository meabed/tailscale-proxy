import { generateStaticParamsFor, importPage } from "nextra/pages";

type PageProps = {
  params: Promise<{ mdxPath?: string[] }>;
};

export const generateStaticParams = generateStaticParamsFor("mdxPath");

export async function generateMetadata(props: PageProps) {
  const params = await props.params;
  if (!params.mdxPath?.length) {
    return {
      title: { absolute: "tailscale-proxy" },
      description:
        "Discover local dev servers by port and expose them through one Tailscale Serve/Funnel entry — an open-source, self-hosted ngrok alternative.",
    };
  }
  const { metadata } = await importPage(params.mdxPath);
  return metadata;
}

export default async function Page(props: PageProps) {
  const params = await props.params;
  const { default: MDXContent } = await importPage(params.mdxPath);
  return <MDXContent {...props} params={params} />;
}
