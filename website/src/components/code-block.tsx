import { useRef, useState } from 'react';

import type { ComponentProps } from 'react';

export function CodeBlock(props: ComponentProps<'pre'>) {
  const ref = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);

  async function copy() {
    await navigator.clipboard.writeText(ref.current?.textContent ?? '');
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  return (
    <div className='group relative'>
      <pre ref={ref} {...props} />
      <button
        type='button'
        onClick={copy}
        data-pagefind-ignore
        className='border-border bg-paper text-muted hover:text-fg-strong absolute top-2 right-2 cursor-pointer rounded-md border px-2 py-0.5 text-[0.75rem] opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100'
      >
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  );
}
