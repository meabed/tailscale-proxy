import { Link } from '@tanstack/react-router';

import { pageGroups } from '@/lib/pages';

export function SiteSidebar() {
  return (
    <nav className='flex flex-col gap-5' aria-label='Documentation'>
      {pageGroups.map((group) => (
        <div key={group.title} className='flex flex-col gap-0.5'>
          <span className='text-fg-strong mb-1 px-3.5 text-[0.8rem] font-semibold'>{group.title}</span>
          {group.pages.map((page) => (
            <Link
              key={page.path}
              to={page.path}
              activeOptions={{ exact: true }}
              className='text-muted hover:bg-paper-2 hover:text-fg-strong data-[status=active]:bg-accent-soft data-[status=active]:text-accent block rounded-[7px] px-3.5 py-[0.4rem] text-[0.92rem] font-medium transition-colors hover:no-underline data-[status=active]:font-semibold'
            >
              {page.title}
            </Link>
          ))}
        </div>
      ))}
    </nav>
  );
}
