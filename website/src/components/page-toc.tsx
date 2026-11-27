import { useEffect, useState } from 'react';

type Heading = { id: string; text: string; level: number };

// "On this page": the page's h2 and h3 (rehype-slug gives them ids), the one being read marked as the reader scrolls.
// Read from the rendered article on mount; the layout keys it by page, so each navigation reads the new page.
export function PageToc() {
  const [headings, setHeadings] = useState<Heading[]>([]);
  const [current, setCurrent] = useState('');

  useEffect(() => {
    const found = [...document.querySelectorAll<HTMLElement>('.tsp-content h2[id], .tsp-content h3[id]')];
    setHeadings(found.map((heading) => ({ id: heading.id, text: heading.textContent ?? '', level: heading.tagName === 'H2' ? 2 : 3 })));
    setCurrent(found[0]?.id ?? '');
    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries.filter((entry) => entry.isIntersecting).toSorted((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0];
        if (visible) setCurrent(visible.target.id);
      },
      { rootMargin: '-64px 0px -70% 0px' },
    );
    for (const heading of found) observer.observe(heading);
    return () => observer.disconnect();
  }, []);

  if (headings.length < 2) return null;
  return (
    <nav aria-label='On this page' className='text-[0.86rem]'>
      <p className='text-fg-strong mb-2 font-semibold'>On this page</p>
      <ul className='border-border border-l'>
        {headings.map((heading) => (
          <li key={heading.id}>
            <a
              href={`#${heading.id}`}
              data-current={heading.id === current || undefined}
              className={`text-muted hover:text-fg-strong data-current:border-accent data-current:text-accent -ml-px block border-l py-1 leading-snug hover:no-underline ${heading.level === 3 ? 'pl-6' : 'pl-3'} border-transparent`}
            >
              {heading.text}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  );
}
