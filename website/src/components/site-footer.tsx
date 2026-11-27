const links = [
  { href: 'https://github.com/meabed/tailscale-proxy', label: 'Source on GitHub' },
  { href: 'https://www.npmjs.com/package/tailscale-proxy', label: 'npm' },
  { href: '/llms.txt', label: 'llms.txt' },
  { href: '/llms-full.txt', label: 'llms-full.txt' },
];

export function SiteFooter() {
  return (
    <footer className='border-border text-muted mt-16 flex flex-wrap items-center justify-between gap-x-6 gap-y-3 border-t pt-8 pb-12 text-[0.88rem]'>
      <p>tailscale-proxy is open source under the MIT license.</p>
      <nav aria-label='Elsewhere' className='flex flex-wrap gap-x-5 gap-y-1'>
        {links.map((link) => (
          <a key={link.href} href={link.href} {...(link.href.startsWith('http') ? { target: '_blank', rel: 'noreferrer' } : {})}>
            {link.label}
          </a>
        ))}
      </nav>
    </footer>
  );
}
