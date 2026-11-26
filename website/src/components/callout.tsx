import type { ReactNode } from 'react';

const calloutTypes = {
  info: {
    className: 'border-accent bg-accent-soft',
    icon: <path d='M8 1a7 7 0 1 0 0 14A7 7 0 0 0 8 1Zm.75 10.5h-1.5V7h1.5v4.5ZM8 5.75a.9.9 0 1 1 0-1.8.9.9 0 0 1 0 1.8Z' />,
  },
  warning: {
    className: 'border-warning bg-warning-soft',
    icon: <path d='M8.87 1.5a1 1 0 0 0-1.74 0L.63 13a1 1 0 0 0 .87 1.5h13a1 1 0 0 0 .87-1.5L8.87 1.5ZM8.75 12h-1.5v-1.5h1.5V12Zm0-3h-1.5V5.5h1.5V9Z' />,
  },
};

export function Callout({ type, children }: { type: keyof typeof calloutTypes; children: ReactNode }) {
  const { className, icon } = calloutTypes[type];
  return (
    <div className={`my-5 flex gap-3 rounded-[9px] border px-4 py-1 ${className}`}>
      <svg className='text-fg-strong mt-[1.05rem] size-4 shrink-0' viewBox='0 0 16 16' fill='currentColor' aria-hidden='true' data-pagefind-ignore>
        {icon}
      </svg>
      <div className='min-w-0 [&>p]:my-3'>{children}</div>
    </div>
  );
}
