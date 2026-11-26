import { Link } from '@tanstack/react-router';

import { pages } from '@/lib/pages';

export function SiteSidebar() {
  return (
    <nav className='flex flex-col gap-0.5' aria-label='Documentation'>
      <span className='text-muted mb-2 px-3.5 text-[0.72rem] tracking-[0.08em] uppercase'>Documentation</span>
      {pages.map((page) => (
        <Link
          key={page.path}
          to={page.path}
          activeOptions={{ exact: true }}
          className='text-muted hover:bg-paper-2 hover:text-fg-strong data-[status=active]:bg-accent-soft data-[status=active]:text-accent block rounded-[7px] px-3.5 py-[0.45rem] text-[0.93rem] font-medium transition-colors hover:no-underline data-[status=active]:font-semibold'
        >
          {page.title}
        </Link>
      ))}
    </nav>
  );
}
