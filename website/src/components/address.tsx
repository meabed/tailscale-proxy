import type { ReactNode } from 'react';

export type Reach = 'local' | 'online';
type Part = { text: string; note: ReactNode };

const reachStyle = {
  local: { tone: 'text-local', rule: 'border-local', soft: 'bg-local-soft', who: 'Only this machine' },
  online: { tone: 'text-accent', rule: 'border-accent', soft: 'bg-accent-soft', who: 'Anyone with the link, or only your tailnet' },
};

function ReachIcon({ reach }: { reach: Reach }) {
  return (
    <svg className='size-[1.05em] shrink-0' viewBox='0 0 24 24' fill='none' stroke='currentColor' strokeWidth='2' strokeLinecap='round' strokeLinejoin='round' aria-hidden='true'>
      {reach === 'local' ? (
        <>
          <rect x='5' y='11' width='14' height='10' rx='2' />
          <path d='M8 11V7a4 4 0 0 1 8 0v4' />
        </>
      ) : (
        <>
          <circle cx='12' cy='12' r='9' />
          <path d='M3 12h18M12 3c2.5 2.7 2.5 15.3 0 18M12 3c-2.5 2.7-2.5 15.3 0 18' />
        </>
      )}
    </svg>
  );
}

// An address taken apart: the URL as a browser shows it, each part underlined, and what each part means below.
export function Address({ reach, parts, command, children }: { reach: Reach; parts: Part[]; command: string; children?: ReactNode }) {
  const style = reachStyle[reach];
  return (
    <figure className='@container my-6 [&_dd]:m-0 [&_p]:my-0'>
      <div className='border-border-strong bg-paper flex items-start gap-3 rounded-xl border px-4 py-3.5 shadow-(--shadow) sm:px-5'>
        <span className={`${style.tone} mt-[0.15em] text-[clamp(0.95rem,2.55cqi,1.55rem)]`}>
          <ReachIcon reach={reach} />
        </span>
        <p className='font-address text-fg-strong min-w-0 text-[clamp(0.85rem,2.55cqi,1.55rem)] leading-[1.45] font-medium tracking-[-0.02em] [overflow-wrap:anywhere]'>
          {parts.map((part, index) => (
            <span key={part.text} className={`border-b-2 pb-0.5 ${index % 2 ? style.rule : `${style.rule} border-dashed`}`}>
              {part.text}
            </span>
          ))}
        </p>
      </div>
      <figcaption className='mt-4'>
        <dl className='grid gap-x-5 gap-y-2.5 text-[0.93rem] leading-snug sm:grid-cols-[max-content_1fr]'>
          {parts.map((part) => (
            <div key={part.text} className='contents'>
              <dt className={`font-address ${style.tone} text-[0.82rem] font-medium max-sm:mt-1.5`}>{part.text}</dt>
              <dd className='text-fg'>{part.note}</dd>
            </div>
          ))}
        </dl>
        <p className='text-muted mt-3.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[0.88rem]'>
          <span className={`${style.soft} ${style.tone} rounded-md px-2 py-0.5 font-medium`}>{style.who}</span>
          <span>
            Made by <code className='font-address text-fg-strong text-[0.82rem]'>{command}</code>
          </span>
          {children}
        </p>
      </figcaption>
    </figure>
  );
}
