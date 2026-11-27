import { Link } from '@tanstack/react-router';

import { pageGroups } from '@/lib/pages';

// The docs, group by group: each group a rule with its pages hanging off it; the page being read takes the accent.
export function SiteSidebar() {
  return (
    <nav className='flex flex-col gap-6' aria-label='Documentation'>
      {pageGroups.map((group) => (
        <div key={group.title}>
          <p className='text-fg-strong mb-2 text-[0.84rem] font-semibold'>{group.title}</p>
          <ul className='border-border border-l'>
            {group.pages.map((page) => (
              <li key={page.path}>
                <Link
                  to={page.path}
                  activeOptions={{ exact: true }}
                  className='text-muted hover:border-border-strong hover:text-fg-strong data-[status=active]:border-accent data-[status=active]:text-accent -ml-px block border-l border-transparent py-[0.32rem] pl-3.5 text-[0.91rem] leading-snug font-medium hover:no-underline data-[status=active]:font-semibold'
                >
                  {page.title}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}
