import { useState } from 'react';

// The part of Pagefind's browser API this uses. Pagefind ships no browser types; the index (`/pagefind/`) is written
// by `pagefind --site dist/client` after the build, so it is missing in `vite dev`.
type PagefindResult = { url: string; excerpt: string; meta: { title?: string } };
type Pagefind = { debouncedSearch: (query: string) => Promise<{ results: { id: string; data: () => Promise<PagefindResult> }[] } | null> };

const pagefindUrl = '/pagefind/pagefind.js';
let pagefind: Promise<Pagefind> | undefined;

export function SiteSearch() {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<PagefindResult[]>([]);
  const [error, setError] = useState('');

  async function search(value: string) {
    setQuery(value);
    pagefind ??= import(/* @vite-ignore */ pagefindUrl);
    const found = await pagefind
      .then((api) => api.debouncedSearch(value))
      .catch(() => {
        pagefind = undefined;
        setError('Search is available in the built site (bun run build).');
        return null;
      });
    if (!found) return;
    setError('');
    setResults(await Promise.all(found.results.slice(0, 8).map((result) => result.data())));
  }

  const open = query.trim() !== '';

  return (
    <div className='relative w-full max-w-64' data-pagefind-ignore onBlur={(event) => !event.currentTarget.contains(event.relatedTarget) && setQuery('')}>
      <input
        type='search'
        value={query}
        onChange={(event) => search(event.target.value)}
        onKeyDown={(event) => event.key === 'Escape' && setQuery('')}
        placeholder='Search docs…'
        aria-label='Search the documentation'
        className='border-border bg-paper-2 text-fg placeholder:text-muted focus:border-accent h-[34px] w-full rounded-lg border px-3 text-[0.9rem] focus:outline-none'
      />
      {open && (
        // Why: keeps focus in the input while a result is clicked, so the blur above doesn't close the list first.
        <div
          onMouseDown={(event) => event.preventDefault()}
          className='border-border bg-paper absolute end-0 top-11 z-50 max-h-[70vh] w-[min(26rem,calc(100vw-2rem))] overflow-y-auto rounded-[9px] border p-2 shadow-(--shadow) max-sm:fixed max-sm:inset-x-4 max-sm:top-[calc(var(--header-h)+0.25rem)] max-sm:w-auto'
        >
          {error || results.length === 0 ? (
            <p className='text-muted m-0 px-3 py-2 text-[0.88rem]'>{error || 'No results.'}</p>
          ) : (
            results.map((result) => (
              <a key={result.url} href={result.url.replace(/\.html$/, '')} className='hover:bg-paper-2 block rounded-[7px] px-3 py-2 hover:no-underline' onClick={() => setQuery('')}>
                <span className='text-fg-strong block font-semibold'>{result.meta.title}</span>
                {/* oxlint-disable-next-line react/no-danger -- Pagefind's excerpt is escaped text from our own pages with <mark> around the matches. */}
                <span className='text-muted [&_mark]:bg-accent-soft [&_mark]:text-fg-strong block text-[0.85rem]' dangerouslySetInnerHTML={{ __html: result.excerpt }} />
              </a>
            ))
          )}
        </div>
      )}
    </div>
  );
}
