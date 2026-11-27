import { Link } from '@tanstack/react-router';

import { pages } from '@/lib/pages';

// Previous and next page in sidebar order, at the foot of every docs page.
export function DocsPager({ pathname }: { pathname: string }) {
  const index = pages.findIndex((page) => page.path === pathname);
  if (index < 0) return null;
  const previous = pages[index - 1];
  const next = pages[index + 1];
  return (
    <nav aria-label='More docs' className='mt-14 grid max-w-(--measure) gap-3 sm:grid-cols-2' data-pagefind-ignore>
      {previous ? (
        <Link to={previous.path} className='border-border hover:border-border-strong group rounded-xl border px-4 py-3 hover:no-underline'>
          <span className='text-muted block text-[0.8rem]'>Previous</span>
          <span className='text-fg-strong group-hover:text-accent font-semibold'>{previous.title}</span>
        </Link>
      ) : (
        <span />
      )}
      {next && (
        <Link to={next.path} className='border-border hover:border-border-strong group rounded-xl border px-4 py-3 text-right hover:no-underline'>
          <span className='text-muted block text-[0.8rem]'>Next</span>
          <span className='text-fg-strong group-hover:text-accent font-semibold'>{next.title}</span>
        </Link>
      )}
    </nav>
  );
}
