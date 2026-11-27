// The figure that explains tsp: two kinds of caller reach the same app on your Mac. A browser on the Mac goes through
// the local HTTPS proxy (green); Stripe, a phone or a teammate go through Tailscale and tsp (blue). Packets travel
// each lane to the app and a reply travels back. Two drawings of the same graph: wide (a row) and narrow (a column);
// the container picks one. Motion is SVG-native (animateMotion) and stops under reduced motion.

type Lane = 'local' | 'online';
type Box = { x: number; y: number; w: number; h: number };
type Node = Box & { lane: Lane | 'app'; title: string; lines: string[]; icon: IconName };
type Route = { lane: Lane; d: string; delay: number };
type Layout = { width: number; height: number; mac: Box; macLabel: { x: number; y: number }; browser: Box; callers: Box[]; nodes: Node[]; routes: Route[] };

type IconName = 'lock' | 'globe' | 'server' | 'card' | 'phone' | 'person' | 'laptop';

const callers: { title: string; detail: string; icon: IconName }[] = [
  { title: 'Stripe webhook', detail: 'POST /api/webhooks', icon: 'card' },
  { title: 'Your phone', detail: 'on mobile data', icon: 'phone' },
  { title: 'A teammate', detail: 'reviewing your branch', icon: 'person' },
];

const tone = {
  local: 'var(--local)',
  online: 'var(--accent)',
  app: 'var(--fg-strong)',
};

const wide: Layout = {
  width: 1120,
  height: 440,
  mac: { x: 380, y: 16, w: 724, h: 408 },
  macLabel: { x: 404, y: 48 },
  browser: { x: 16, y: 40, w: 304, h: 136 },
  callers: [
    { x: 16, y: 252, w: 276, h: 46 },
    { x: 16, y: 310, w: 276, h: 46 },
    { x: 16, y: 368, w: 276, h: 46 },
  ],
  nodes: [
    { lane: 'local', x: 470, y: 73, w: 210, h: 76, title: 'tsp proxy', lines: ['port 443, from boot', 'a certificate per name'], icon: 'lock' },
    { lane: 'online', x: 410, y: 295, w: 236, h: 76, title: 'Tailscale Funnel', lines: ['bigfoot.tail1234.ts.net', 'TLS ends here'], icon: 'globe' },
    { lane: 'online', x: 682, y: 295, w: 168, h: 76, title: 'tsp', lines: ['/api/ → project api', 'found by its port'], icon: 'server' },
    { lane: 'app', x: 900, y: 172, w: 188, h: 96, title: 'api', lines: ['bun dev', '127.0.0.1:4417'], icon: 'server' },
  ],
  routes: [
    { lane: 'local', d: 'M320 111 H470 M680 111 C810 111 810 200 900 200', delay: 0 },
    { lane: 'online', d: 'M292 275 C351 275 351 333 410 333 H682 M850 333 C880 333 866 240 900 240', delay: 0.9 },
    { lane: 'online', d: 'M292 333 H410 H682 M850 333 C880 333 866 240 900 240', delay: 2.1 },
    { lane: 'online', d: 'M292 391 C351 391 351 333 410 333 H682 M850 333 C880 333 866 240 900 240', delay: 3.3 },
  ],
};

const narrow: Layout = {
  width: 420,
  height: 820,
  mac: { x: 4, y: 236, w: 412, h: 576 },
  macLabel: { x: 24, y: 268 },
  browser: { x: 4, y: 8, w: 200, h: 120 },
  callers: [
    { x: 216, y: 8, w: 200, h: 62 },
    { x: 216, y: 80, w: 200, h: 62 },
    { x: 216, y: 152, w: 200, h: 62 },
  ],
  nodes: [
    { lane: 'local', x: 15, y: 300, w: 178, h: 74, title: 'tsp proxy', lines: ['port 443, from boot', 'a cert per name'], icon: 'lock' },
    { lane: 'online', x: 227, y: 300, w: 178, h: 74, title: 'Tailscale Funnel', lines: ['bigfoot.tail1234', 'TLS ends here'], icon: 'globe' },
    { lane: 'online', x: 227, y: 450, w: 178, h: 74, title: 'tsp', lines: ['/api/ → api', 'found by its port'], icon: 'server' },
    { lane: 'app', x: 110, y: 660, w: 200, h: 96, title: 'api', lines: ['bun dev', '127.0.0.1:4417'], icon: 'server' },
  ],
  routes: [
    { lane: 'local', d: 'M104 128 V300 M104 374 V560 C104 610 160 620 160 660', delay: 0 },
    { lane: 'online', d: 'M316 214 V300 M316 374 V450 M316 524 V560 C316 610 260 620 260 660', delay: 1.2 },
  ],
};

const iconPaths: Record<IconName, string> = {
  lock: 'M7 11V8a5 5 0 0 1 10 0v3M6 11h12v9H6z',
  globe: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18M3 12h18M12 3c2.6 2.8 2.6 15.2 0 18M12 3c-2.6 2.8-2.6 15.2 0 18',
  server: 'M5 4h14v7H5zM5 13h14v7H5zM8 7.5h.01M8 16.5h.01',
  card: 'M3 6h18v12H3zM3 10h18M7 15h4',
  phone: 'M8 2h8a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H8a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1zM11 18h2',
  person: 'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21a8 8 0 0 1 16 0',
  laptop: 'M5 5h14v10H5zM2 19h20',
};

// A box as SVG rect attributes.
function rectOf({ x, y, w, h }: Box) {
  return { x, y, width: w, height: h };
}

function Icon({ name, x, y, size, color }: { name: IconName; x: number; y: number; size: number; color: string }) {
  return <path d={iconPaths[name]} transform={`translate(${x} ${y}) scale(${size / 24})`} fill='none' stroke={color} strokeWidth={(1.8 * 24) / size} strokeLinecap='round' strokeLinejoin='round' vectorEffect='non-scaling-stroke' />;
}

function Drawing({ layout, className }: { layout: Layout; className: string }) {
  const { browser, mac } = layout;
  const big = layout === wide;
  return (
    <svg viewBox={`0 0 ${layout.width} ${layout.height}`} className={className} role='img' aria-hidden='true'>
      {/* Your Mac: everything tsp runs is inside this outline. */}
      <rect {...rectOf(mac)} rx={22} fill='var(--paper-2)' stroke='var(--border-strong)' strokeDasharray='6 6' />
      <Icon name='laptop' x={layout.macLabel.x} y={layout.macLabel.y - 16} size={20} color='var(--muted)' />
      <text x={layout.macLabel.x + 28} y={layout.macLabel.y} fill='var(--muted)' fontSize={14} fontWeight={600}>
        Your Mac
      </text>

      {/* The lanes, under the nodes: a quiet track, and a brighter dash that drifts toward the app. */}
      {layout.routes.map((route) => (
        <g key={route.d}>
          <path d={route.d} fill='none' stroke={tone[route.lane]} strokeOpacity={0.22} strokeWidth={6} strokeLinecap='round' />
          <path d={route.d} fill='none' stroke={tone[route.lane]} strokeWidth={1.6} strokeDasharray='2 9' strokeLinecap='round' className='flow-dash' />
        </g>
      ))}

      {/* Requests travel to the app, passing under each node that handles them; each reply comes back on the same track,
          smaller. */}
      {layout.routes.map((route) => (
        <g key={`packets-${route.d}`} className='flow-packets'>
          <circle r={5.5} fill={tone[route.lane]} opacity={0}>
            <animateMotion dur='3.6s' begin={`${route.delay}s`} repeatCount='indefinite' path={route.d.replaceAll(/ M/g, ' L')} keyPoints='0;1' keyTimes='0;1' calcMode='linear' />
            <animate attributeName='opacity' values='0;1;1;0' keyTimes='0;0.08;0.9;1' dur='3.6s' begin={`${route.delay}s`} repeatCount='indefinite' />
          </circle>
          <circle r={3} fill={tone[route.lane]} opacity={0}>
            <animateMotion dur='3.6s' begin={`${route.delay + 1.8}s`} repeatCount='indefinite' path={route.d.replaceAll(/ M/g, ' L')} keyPoints='1;0' keyTimes='0;1' calcMode='linear' />
            <animate attributeName='opacity' values='0;0.85;0.85;0' keyTimes='0;0.1;0.88;1' dur='3.6s' begin={`${route.delay + 1.8}s`} repeatCount='indefinite' />
          </circle>
        </g>
      ))}
      {/* A browser window on this Mac, with the local name in its address bar. */}
      <g>
        <rect {...rectOf(browser)} rx={12} fill='var(--paper)' stroke='var(--border-strong)' />
        {[0, 1, 2].map((dot) => (
          <circle key={dot} cx={browser.x + 18 + dot * 13} cy={browser.y + 17} r={4} fill='var(--border-strong)' />
        ))}
        <rect x={browser.x + 12} y={browser.y + 32} width={browser.w - 24} height={30} rx={15} fill='var(--paper-2)' stroke='var(--border)' />
        <Icon name='lock' x={browser.x + 21} y={browser.y + 39} size={15} color='var(--local)' />
        <text x={browser.x + 42} y={browser.y + 52} fill='var(--fg-strong)' fontSize={big ? 12 : 11} fontFamily='var(--font-address)' fontWeight={500}>
          {big ? 'https://api.acme.localhost' : 'api.acme.localhost'}
        </text>
        <rect x={browser.x + 14} y={browser.y + 78} width={browser.w * 0.62} height={8} rx={4} fill='var(--paper-3)' />
        <rect x={browser.x + 14} y={browser.y + 94} width={browser.w * 0.45} height={8} rx={4} fill='var(--paper-3)' />
        <text x={browser.x + 14} y={browser.y + browser.h - 10} fill='var(--muted)' fontSize={12}>
          Browser on this Mac
        </text>
      </g>

      {/* Callers from anywhere: each one a row with what it is and what it sends. */}
      {layout.callers.map((box, index) => {
        const caller = callers[index];
        if (!caller) return null;
        return (
          <g key={caller.title}>
            <rect {...rectOf(box)} rx={10} fill='var(--paper)' stroke='var(--border-strong)' />
            <Icon name={caller.icon} x={box.x + 12} y={box.y + box.h / 2 - 10} size={20} color='var(--accent)' />
            <text x={box.x + 42} y={box.y + box.h / 2 - 3} fill='var(--fg-strong)' fontSize={13} fontWeight={600}>
              {caller.title}
            </text>
            <text x={box.x + 42} y={box.y + box.h / 2 + 13} fill='var(--muted)' fontSize={11.5}>
              {caller.detail}
            </text>
          </g>
        );
      })}

      {/* tsp, Tailscale and the app. */}
      {layout.nodes.map((node) => (
        <g key={node.title}>
          <rect x={node.x} y={node.y} width={node.w} height={node.h} rx={12} fill='var(--paper)' stroke={node.lane === 'app' ? 'var(--fg-strong)' : tone[node.lane]} strokeWidth={node.lane === 'app' ? 1.6 : 1.3} />
          <Icon name={node.icon} x={node.x + 12} y={node.y + 12} size={18} color={tone[node.lane]} />
          <text x={node.x + 38} y={node.y + 26} fill='var(--fg-strong)' fontSize={node.lane === 'app' ? 16 : 14} fontWeight={650}>
            {node.title}
          </text>
          {node.lines.map((line, index) => (
            <text
              key={line}
              x={node.x + 14}
              y={node.y + 26 + (node.lane === 'app' ? 26 : 20) + index * 16}
              fill='var(--muted)'
              fontSize={12}
              fontFamily={line.includes('127.0.0.1') || line.includes('ts.net') || line.includes('tail1234') ? 'var(--font-address)' : undefined}
            >
              {line}
            </text>
          ))}
        </g>
      ))}
    </svg>
  );
}

// The figure with its caption: wide drawing in a wide container, narrow drawing otherwise.
export function RequestFlow() {
  return (
    <figure className='@container my-8'>
      <div className='border-border bg-paper rounded-2xl border p-3 shadow-(--shadow) sm:p-5'>
        <Drawing layout={wide} className='hidden h-auto w-full @4xl:block' />
        <Drawing layout={narrow} className='mx-auto block h-auto w-full max-w-[26rem] @4xl:hidden' />
      </div>
      <figcaption className='text-muted mt-3 flex flex-wrap gap-x-5 gap-y-1.5 text-[0.88rem]'>
        <span className='inline-flex items-center gap-2'>
          <span className='size-2.5 rounded-full bg-local' aria-hidden='true' />
          <span>
            <span className='text-fg-strong font-medium'>Local:</span> only this Mac, through the tsp proxy on port 443
          </span>
        </span>
        <span className='inline-flex items-center gap-2'>
          <span className='bg-accent size-2.5 rounded-full' aria-hidden='true' />
          <span>
            <span className='text-fg-strong font-medium'>Online:</span> anyone with the link, through Tailscale, then tsp
          </span>
        </span>
      </figcaption>
      <p className='sr-only'>
        A browser on your Mac opens https://api.acme.localhost; the tsp proxy on port 443 passes the request to the app on 127.0.0.1:4417. A Stripe webhook, your phone or a teammate opens https://bigfoot.tail1234.ts.net/api/; Tailscale
        Funnel ends TLS on your Mac and tsp routes /api/ to the same app.
      </p>
    </figure>
  );
}
