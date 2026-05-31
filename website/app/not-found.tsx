import Link from "next/link";

export default function NotFound() {
  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        minHeight: "60vh",
        gap: "1rem",
        textAlign: "center",
      }}
    >
      <h1>404 — Page not found</h1>
      <p>That page doesn’t exist (yet).</p>
      <Link href="/">← Back to the docs</Link>
    </div>
  );
}
