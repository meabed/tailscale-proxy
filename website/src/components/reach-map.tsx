import type { ReactNode } from 'react';

type Hop = { title: string; detail: string };
type Lane = { reach: 'local' | 'online'; who: string; whoDetail: string; address: string; hops: Hop[] };

const lanes: Lane[] = [
  {
    reach: 'local',
    who: 'You, on this machine',
    whoDetail: 'browser, curl, other apps',
    address: 'https://api.acme.localhost',
    hops: [{ title: 'tsp proxy', detail: 'port 443, from boot; signs a certificate per name' }],
  },
  {
    reach: 'online',
    who: 'Anyone you give the link',
    whoDetail: 'Stripe, GitHub, your phone, a teammate',
    address: 'https://bigfoot.tail1234.ts.net/api/',
    hops: [
      { title: 'Tailscale', detail: 'Funnel (public) or Serve (tailnet); TLS ends here' },
      { title: 'tsp', detail: 'finds dev servers by port, routes /api/ to its port' },
    ],
  },
];

const tone = {
  local: { text: 'text-local', line: 'bg-local', border: 'border-local', soft: 'bg-local-soft' },
  online: { text: 'text-accent', line: 'bg-accent', border: 'border-accent', soft: 'bg-accent-soft' },
};

function Box({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <div className={`border-border-strong bg-paper relative z-10 rounded-lg border px-3 py-2 ${className}`}>{children}</div>;
}

// Who can reach an app, and through what: the local lane (this machine) and the online lane (Tailscale) end at the
// same app. Rows when the figure is wide (896px and up), a column per lane when it is narrow.
export function ReachMap() {
  return (
    <figure className='@container my-8 [&_p]:my-0' aria-label='Two ways to reach the same app: a .localhost name on this machine, and a ts.net URL online through Tailscale'>
      <div className='grid gap-4 @4xl:grid-cols-[1fr_1.5rem_auto] @4xl:items-center @4xl:gap-0'>
        <div className='grid gap-4'>
          {lanes.map((lane) => {
            const color = tone[lane.reach];
            return (
              <div key={lane.reach} className={`relative grid gap-2 rounded-xl border-l-4 ${color.border} ${color.soft} p-3 @4xl:grid-cols-[11rem_1fr_auto] @4xl:items-center @4xl:gap-3`}>
                <Box>
                  <p className='text-fg-strong text-[0.92rem] font-semibold'>{lane.who}</p>
                  <p className='text-muted text-[0.8rem] leading-snug'>{lane.whoDetail}</p>
                </Box>
                <div className='relative flex items-center'>
                  <span className={`absolute inset-x-0 top-1/2 hidden h-0.5 -translate-y-1/2 @4xl:block ${color.line}`} aria-hidden='true' />
                  <code className={`font-address ${color.text} bg-paper relative z-10 rounded-md px-2 py-1 text-[0.8rem] font-medium break-all @4xl:mx-auto`}>{lane.address}</code>
                </div>
                <div className='flex flex-wrap items-stretch gap-2 @4xl:flex-nowrap'>
                  {lane.hops.map((hop) => (
                    <Box key={hop.title} className='@4xl:w-[10.5rem]'>
                      <p className={`${color.text} text-[0.88rem] font-semibold`}>{hop.title}</p>
                      <p className='text-muted text-[0.78rem] leading-snug'>{hop.detail}</p>
                    </Box>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
        {/* Joins the two lanes into the app: from the middle of each lane to the app's box. */}
        <div className='relative hidden self-stretch @4xl:block' aria-hidden='true'>
          <span className='border-border-strong absolute inset-y-[22%] left-0 w-1/2 rounded-r-md border-y-2 border-r-2' />
          <span className='bg-border-strong absolute top-1/2 right-0 h-0.5 w-1/2' />
        </div>
        <Box className='@4xl:w-[11.5rem] @4xl:py-4'>
          <p className='text-fg-strong text-[0.92rem] font-semibold'>Your app</p>
          <p className='text-muted text-[0.8rem] leading-snug'>
            plain HTTP on <span className='font-address text-[0.74rem]'>127.0.0.1:4417</span>, started the way you always start it
          </p>
        </Box>
      </div>
    </figure>
  );
}
