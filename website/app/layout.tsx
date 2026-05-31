import Link from "next/link";
import { Head } from "nextra/components";
import "nextra-theme-docs/style.css";
import "./globals.css";
import { Sidebar } from "./nav";

export const metadata = {
  title: {
    default: "tailscale-proxy — self-hosted ngrok alternative on Tailscale",
    template: "%s – tailscale-proxy",
  },
  description:
    "Discover local dev servers by port and expose them through one Tailscale Serve/Funnel entry, routed by project name. An open-source, self-hosted ngrok alternative.",
  metadataBase: new URL("https://tailscale-proxy.vercel.app"),
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" dir="ltr" suppressHydrationWarning>
      <Head />
      <body>
        <header className="tsp-header">
          <Link href="/" className="tsp-brand">
            tailscale-proxy
          </Link>
          <nav className="tsp-topnav">
            <a href="https://github.com/meabed/tailscale-proxy" target="_blank" rel="noreferrer">
              GitHub
            </a>
            <a href="https://www.npmjs.com/package/tailscale-proxy" target="_blank" rel="noreferrer">
              npm
            </a>
          </nav>
        </header>
        <div className="tsp-shell">
          <aside className="tsp-aside">
            <Sidebar />
          </aside>
          <main className="tsp-main">
            <article className="tsp-content">{children}</article>
            <footer className="tsp-footer">
              MIT © {new Date().getFullYear()}{" "}
              <a href="https://meabed.com" target="_blank" rel="noreferrer">
                Mohamed Meabed
              </a>
            </footer>
          </main>
        </div>
      </body>
    </html>
  );
}
