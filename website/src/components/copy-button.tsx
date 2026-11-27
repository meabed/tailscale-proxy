import { useState } from 'react';

// Copies `text` and says so for a moment.
export function CopyButton({ text, label = 'Copy', className = '' }: { text: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    await navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 1600);
  }

  return (
    <button type='button' onClick={copy} data-pagefind-ignore className={`border-border-strong bg-paper text-fg-strong hover:bg-paper-3 cursor-pointer rounded-md border px-2.5 py-1 text-[0.8rem] font-medium transition-colors ${className}`}>
      <span aria-live='polite'>{copied ? 'Copied' : label}</span>
    </button>
  );
}
