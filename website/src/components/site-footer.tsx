// Read once per build: every page is prerendered.
const year = new Date().getFullYear();

export function SiteFooter() {
  return (
    <footer className='border-border text-muted mt-12 max-w-(--measure) border-t pt-7 text-center text-[0.88rem]'>
      MIT © {year} ·{' '}
      <a href='https://meabed.com' target='_blank' rel='noreferrer'>
        Mohamed Meabed
      </a>{' '}
      ·{' '}
      <a href='https://github.com/meabed/tailscale-proxy' target='_blank' rel='noreferrer'>
        Source
      </a>
    </footer>
  );
}
