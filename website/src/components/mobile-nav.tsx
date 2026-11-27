import { useLocation } from '@tanstack/react-router';
import { useEffect, useState } from 'react';

import { SiteSidebar } from '@/components/site-sidebar';

// Below 860px the sidebar is hidden; this button opens the same page list under the header. It closes on the next
// page, on Escape and on a second press.
export function MobileNav() {
  const pathname = useLocation({ select: (location) => location.pathname });
  // The page the list was opened on: on any other page it is closed.
  const [openOn, setOpenOn] = useState<string>();
  const open = openOn === pathname;

  useEffect(() => {
    if (!open) return undefined;
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === 'Escape') setOpenOn(undefined);
    }
    document.addEventListener('keydown', closeOnEscape);
    return () => document.removeEventListener('keydown', closeOnEscape);
  }, [open]);

  return (
    <div className='min-[861px]:hidden'>
      <button
        type='button'
        onClick={() => setOpenOn(open ? undefined : pathname)}
        aria-expanded={open}
        aria-controls='mobile-docs-nav'
        aria-label={open ? 'Close the page list' : 'Open the page list'}
        className='border-border bg-paper-2 text-muted hover:border-border-strong hover:bg-paper-3 hover:text-fg-strong inline-grid size-[34px] cursor-pointer place-items-center rounded-lg border transition-colors'
      >
        <svg className='size-[17px]' viewBox='0 0 24 24' fill='none' stroke='currentColor' strokeWidth='2' strokeLinecap='round' aria-hidden='true'>
          {open ? <path d='M6 6l12 12M18 6L6 18' /> : <path d='M4 7h16M4 12h16M4 17h16' />}
        </svg>
      </button>
      <div id='mobile-docs-nav' hidden={!open} className='border-border bg-paper absolute inset-x-0 top-full max-h-[calc(100dvh-var(--header-h))] overflow-y-auto border-b px-4 py-5 shadow-(--shadow) sm:px-7'>
        <SiteSidebar />
      </div>
    </div>
  );
}
