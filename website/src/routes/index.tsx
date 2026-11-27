import { createFileRoute, Link } from '@tanstack/react-router';

import { Address } from '@/components/address';
import { AgentPrompt } from '@/components/agent-prompt';
import { CopyButton } from '@/components/copy-button';
import { RequestFlow } from '@/components/request-flow';

import type { ReactNode } from 'react';

export const Route = createFileRoute('/')({
  component: Home,
});

const install = 'brew install meabed/tap/tsp';

const uses: { problem: string; detail: string; answer: string; reach: 'local' | 'online'; to: string; more: string }[] = [
  {
    problem: 'Stripe, GitHub or Slack has to call your laptop',
    detail: 'Register the endpoint once. It is the same URL tomorrow, after a reboot, on any network.',
    answer: 'https://bigfoot.tail1234.ts.net/api/webhooks/stripe',
    reach: 'online',
    to: '/webhooks',
    more: 'Webhooks',
  },
  {
    problem: 'Your sign-in provider wants a fixed callback URL',
    detail: 'Google and GitHub accept it as a redirect URI, like a deployed site.',
    answer: 'https://bigfoot.tail1234.ts.net/web/auth/callback',
    reach: 'online',
    to: '/webhooks',
    more: 'OAuth callbacks',
  },
  {
    problem: 'Secure cookies, service workers and passkeys need HTTPS',
    detail: 'A trusted certificate on your own machine, so the browser treats the app like production.',
    answer: 'https://app.acme.localhost',
    reach: 'local',
    to: '/local-https',
    more: 'Local HTTPS names',
  },
  {
    problem: 'Three services, and nobody remembers which port is which',
    detail: 'Each app gets a name; tsp picks a free port and the name never changes.',
    answer: 'https://api.acme.localhost',
    reach: 'local',
    to: '/local-https',
    more: 'Names instead of ports',
  },
  {
    problem: 'You want the app on your phone, or in front of a teammate',
    detail: 'Private to your Tailscale network, or public when someone outside needs it.',
    answer: 'tsp --private',
    reach: 'online',
    to: '/share',
    more: 'Share online',
  },
  {
    problem: 'An AI agent starts servers and loses track of them',
    detail: 'One command lists every running app and its address, as JSON.',
    answer: 'tsp routes --json',
    reach: 'local',
    to: '/agents',
    more: 'Use with AI agents',
  },
];

function Section({ title, lead, children }: { title: string; lead?: ReactNode; children: ReactNode }) {
  return (
    <section className='border-border mt-24 border-t pt-12'>
      <div className='grid gap-x-12 gap-y-3 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)] lg:items-end'>
        <h2 className='text-fg-strong text-[clamp(1.6rem,1.2rem+1.3vw,2.25rem)] leading-[1.1] font-bold tracking-[-0.025em]'>{title}</h2>
        {lead && <p className='text-fg max-w-[64ch] text-[1.05rem] leading-relaxed'>{lead}</p>}
      </div>
      {children}
    </section>
  );
}

function Step({ title, children }: { title: string; children: ReactNode }) {
  return (
    <li className='before:border-border-strong before:text-fg-strong relative min-w-0 pl-11 [counter-increment:step] before:absolute before:top-0 before:left-0 before:grid before:size-7 before:place-items-center before:rounded-full before:border before:text-[0.85rem] before:font-semibold before:content-[counter(step)]'>
      <h3 className='text-fg-strong text-[1.05rem] font-semibold'>{title}</h3>
      <div className='text-fg mt-1.5 min-w-0 [&_pre]:mt-2.5'>{children}</div>
    </li>
  );
}

function Command({ children }: { children: string }) {
  return (
    <pre className='border-border flex items-center justify-between gap-3 rounded-lg border bg-(--code-bg) px-3.5 py-2.5 font-mono text-[0.86rem] text-(--code-fg)'>
      <code className='min-w-0 overflow-x-auto whitespace-pre'>{children}</code>
      <CopyButton text={children.replace(/\s+#.*$/gm, '')} className='shrink-0' />
    </pre>
  );
}

// What the two commands print, as tsp prints them: a name on this machine, then the online links.
const terminal: { prompt?: string; command?: string; out?: string; tone?: 'local' | 'online' | 'muted' }[] = [
  { prompt: '~/acme/web', command: 'bun dev' },
  { out: 'tsp: https://web.acme.localhost → 127.0.0.1:4417', tone: 'local' },
  { out: '  VITE ready in 412 ms', tone: 'muted' },
  {},
  { prompt: '~/acme', command: 'tsp' },
  { out: 'public (Funnel) → 127.0.0.1:18443 (port 443)', tone: 'muted' },
  {},
  { out: 'Services:' },
  { out: '  https://bigfoot.tail1234.ts.net/web/  →  127.0.0.1:4417', tone: 'online' },
  { out: '  https://bigfoot.tail1234.ts.net/api/  →  127.0.0.1:4100', tone: 'online' },
];

const lineTone = { local: 'text-local', online: 'text-accent', muted: 'text-muted' };

function HeroTerminal() {
  return (
    <figure className='border-border-strong overflow-hidden rounded-xl border bg-(--code-bg) shadow-(--shadow)' aria-label='A terminal: bun dev prints the local name, tsp prints the online links'>
      <div className='border-border flex items-center gap-1.5 border-b px-3.5 py-2.5' aria-hidden='true'>
        {[0, 1, 2].map((dot) => (
          <span key={dot} className='bg-border-strong size-2.5 rounded-full' />
        ))}
        <span className='text-muted ml-2 text-[0.78rem]'>Terminal</span>
      </div>
      <pre className='overflow-x-auto px-4 py-4 font-mono text-[0.8rem] leading-[1.75] text-(--code-fg)'>
        {terminal.map((line, index) => (
          // oxlint-disable-next-line react/no-array-index-key -- a fixed transcript: lines never move.
          <span key={index} className='block min-h-[1.75em]'>
            {line.command ? (
              <>
                <span className='text-muted'>{line.prompt} $ </span>
                <span className='text-fg-strong font-semibold'>{line.command}</span>
              </>
            ) : (
              <span className={line.tone ? lineTone[line.tone] : ''}>{line.out}</span>
            )}
          </span>
        ))}
        <span className='terminal-cursor bg-fg-strong inline-block h-[1.1em] w-[0.55em] translate-y-[0.2em]' aria-hidden='true' />
      </pre>
    </figure>
  );
}

function Home() {
  return (
    <div className='pt-14 pb-20 max-sm:pt-9'>
      <header className='grid items-center gap-x-14 gap-y-10 lg:grid-cols-[minmax(0,1fr)_minmax(0,0.95fr)]'>
        <div>
          <h1 className='text-fg-strong max-w-[17ch] text-[clamp(2.3rem,1.4rem+3.6vw,3.9rem)] leading-[1.02] font-bold tracking-[-0.035em]'>Real HTTPS addresses for your local dev servers</h1>
          <p className='text-fg mt-5 max-w-[58ch] text-[clamp(1.05rem,0.98rem+0.3vw,1.2rem)] leading-relaxed'>
            tsp gives each app a name on your machine and, when you need one, a link that works from anywhere: for webhooks, OAuth callbacks, your phone and your teammates. One small open-source binary, on top of Tailscale.
          </p>
          <div className='mt-7 flex flex-wrap items-center gap-3'>
            <div className='border-border-strong bg-paper flex items-center gap-3 rounded-lg border py-1.5 pr-1.5 pl-3.5 shadow-(--shadow)'>
              <code className='font-address text-fg-strong text-[0.86rem]'>{install}</code>
              <CopyButton text={install} />
            </div>
            <Link to='/installation' className='text-[0.95rem] font-semibold'>
              Install and set up
            </Link>
            <Link to='/desktop' className='text-muted hover:text-fg-strong text-[0.95rem] font-medium'>
              or get the desktop app
            </Link>
          </div>
        </div>
        <HeroTerminal />
      </header>

      <RequestFlow />

      <Section
        title='Two addresses, taken apart'
        lead={
          <>
            Your app keeps listening on a plain local port. tsp puts a name in front of it on your machine, and Tailscale carries the online link to the same port. Use either side alone, or both. The{' '}
            <Link to='/how-it-works'>technical details</Link> are a page of their own.
          </>
        }
      >
        <div className='mt-10 grid gap-y-10'>
          <Address
            reach='local'
            command='tsp run'
            parts={[
              { text: 'https://', note: 'A real certificate your browser and curl trust, signed on this machine.' },
              { text: 'api.acme', note: 'The name you give the app in package.json. You never type a port again.' },
              { text: '.localhost', note: 'Reachable only from this machine.' },
            ]}
          />
          <Address
            reach='online'
            command='tsp'
            parts={[
              { text: 'https://', note: "Tailscale's certificate, valid everywhere." },
              { text: 'bigfoot.tail1234.ts.net', note: 'Your machine on your Tailscale network. It stays the same, so links you register keep working.' },
              { text: '/api/', note: 'The project folder the dev server runs from, found by port.' },
              { text: 'webhooks/stripe', note: 'The rest of the path reaches your app as is.' },
            ]}
          />
        </div>
      </Section>

      <Section title='What it is for' lead='Pick the problem you have. Each one has an address that solves it.'>
        <ul className='border-border mt-10 divide-y divide-(--border) border-y'>
          {uses.map((use) => (
            <li key={use.problem} className='grid gap-x-8 gap-y-2 py-5 md:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)] md:items-center'>
              <div>
                <h3 className='text-fg-strong text-[1.04rem] leading-snug font-semibold'>{use.problem}</h3>
                <p className='text-muted mt-1 text-[0.93rem] leading-snug'>
                  {use.detail}{' '}
                  <Link to={use.to} className='font-medium whitespace-nowrap'>
                    {use.more}
                  </Link>
                </p>
              </div>
              <code className={`font-address rounded-md px-2.5 py-1.5 text-[0.8rem] font-medium break-all md:justify-self-start ${use.reach === 'local' ? 'text-local bg-local-soft' : 'text-accent bg-accent-soft'}`}>{use.answer}</code>
            </li>
          ))}
        </ul>
      </Section>

      <Section title='Start in two minutes'>
        <ol className='mt-10 grid gap-x-14 gap-y-12 [counter-reset:step] lg:grid-cols-2'>
          <Step title='Install tsp'>
            <p>Homebrew on macOS and Linux, or npm anywhere. Windows: see Install.</p>
            <Command>{install}</Command>
            <Command>npm i -g tailscale-proxy</Command>
          </Step>
          <Step title='Set up local HTTPS, once per machine'>
            <p>It starts the local proxy from boot and trusts its certificate. Run it again any time: it checks what is in place and fixes only what is missing.</p>
            <Command>tsp service install</Command>
          </Step>
          <Step title='Run an app at its name'>
            <p>
              Name the app in <code>package.json</code> and keep your real dev command as another script. It must listen on <code>$PORT</code>.
            </p>
            <pre className='border-border mt-2.5 overflow-x-auto rounded-lg border bg-(--code-bg) px-3.5 py-2.5 font-mono text-[0.84rem] text-(--code-fg)'>{`"scripts": { "dev": "tsp run", "dev:app": "vite --port $PORT --host $HOST" },
"tsp": { "name": "web.acme", "script": "dev:app" }`}</pre>
            <Command>{'bun dev            # https://web.acme.localhost'}</Command>
          </Step>
          <Step title='Share it online when you need to'>
            <p>
              With Tailscale signed in (<code>tsp doctor</code> checks it), one command publishes every running dev server and prints the links. Ctrl-C takes them down.
            </p>
            <Command>{'tsp                # https://bigfoot.tail1234.ts.net/web/'}</Command>
          </Step>
        </ol>
      </Section>

      <Section
        title='Tell your agent'
        lead={
          <>
            Coding agents follow the same steps. Paste this before asking one to run or share an app, or point it at <a href='/llms.txt'>llms.txt</a> and <a href='/llms-full.txt'>llms-full.txt</a>. More on the{' '}
            <Link to='/agents'>agents page</Link>.
          </>
        }
      >
        <AgentPrompt />
      </Section>
    </div>
  );
}
