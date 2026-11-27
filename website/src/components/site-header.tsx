import { Link } from '@tanstack/react-router';

import { MobileNav } from '@/components/mobile-nav';
import { SiteSearch } from '@/components/site-search';
import { ThemeToggle } from '@/components/theme-toggle';
import { siteName } from '@/lib/pages';

export function SiteHeader() {
  return (
    <header className='border-border bg-paper/82 sticky top-0 z-50 border-b backdrop-blur-[10px] backdrop-saturate-[1.8]'>
      <div className='mx-auto flex h-(--header-h) max-w-(--shell) items-center justify-between gap-3 px-4 sm:gap-4 sm:px-7'>
        <Link to='/' className='text-fg-strong inline-flex shrink-0 items-center gap-[0.55rem] text-[1.02rem] font-[650] tracking-[-0.01em] hover:no-underline'>
          <span className='bg-accent size-2.5 rounded-full shadow-[0_0_0_4px_var(--accent-soft)]' aria-hidden='true' />
          {siteName}
        </Link>
        <nav className='flex min-w-0 items-center justify-end gap-3 sm:gap-4' aria-label='Primary'>
          <SiteSearch />
          <a href='https://www.npmjs.com/package/tailscale-proxy' target='_blank' rel='noreferrer' className='text-muted hover:text-fg-strong text-[0.92rem] font-medium hover:no-underline max-sm:hidden'>
            npm
          </a>
          <a href='https://github.com/meabed/tailscale-proxy' target='_blank' rel='noreferrer' aria-label='GitHub repository' title='GitHub' className='text-muted hover:text-fg-strong'>
            <svg width='19' height='19' viewBox='0 0 16 16' fill='currentColor' aria-hidden='true'>
              <path d='M8 0a8 8 0 0 0-2.53 15.59c.4.07.55-.17.55-.38v-1.35c-2.23.49-2.7-1.07-2.7-1.07-.36-.92-.89-1.17-.89-1.17-.73-.5.05-.49.05-.49.8.06 1.23.83 1.23.83.72 1.23 1.87.87 2.33.67.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.83-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.6 7.6 0 0 1 4 0c1.53-1.03 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.52.56.83 1.28.83 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48v2.2c0 .21.15.46.55.38A8 8 0 0 0 8 0z' />
            </svg>
          </a>
          <ThemeToggle />
          <MobileNav />
        </nav>
      </div>
    </header>
  );
}
