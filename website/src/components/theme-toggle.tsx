import { useEffect, useState } from 'react';

const storageKey = 'tsp-theme';

// Runs in <head> before first paint so there is no flash: applies the stored choice, otherwise the CSS
// prefers-color-scheme default stays in charge. The `dark` class also drives Mermaid's theme.
export const themeScript = `(function(){try{var d=document.documentElement,t=localStorage.getItem('${storageKey}');if(t==='dark'||t==='light'){d.dataset.theme=t}var dark=t?t==='dark':matchMedia('(prefers-color-scheme: dark)').matches;d.classList.toggle('dark',dark);d.classList.toggle('light',!dark)}catch(e){}})();`;

export function ThemeToggle() {
  const [dark, setDark] = useState(false);

  useEffect(() => {
    setDark(document.documentElement.classList.contains('dark'));
  }, []);

  function toggle() {
    const next = dark ? 'light' : 'dark';
    const root = document.documentElement;
    root.dataset.theme = next;
    root.classList.toggle('dark', !dark);
    root.classList.toggle('light', dark);
    setDark(!dark);
    try {
      localStorage.setItem(storageKey, next);
    } catch {
      // Storage can be blocked (private mode); the choice then lasts for this page only.
    }
  }

  return (
    <button
      type='button'
      onClick={toggle}
      className='border-border bg-paper-2 text-muted hover:border-border-strong hover:bg-paper-3 hover:text-fg-strong inline-grid size-[34px] cursor-pointer place-items-center rounded-lg border transition-colors'
      aria-label={dark ? 'Switch to light theme' : 'Switch to dark theme'}
      title='Toggle light / dark'
    >
      {dark ? (
        <svg className='size-[17px]' viewBox='0 0 24 24' fill='none' stroke='currentColor' strokeWidth='2' strokeLinecap='round' aria-hidden='true'>
          <circle cx='12' cy='12' r='4' />
          <path d='M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4' />
        </svg>
      ) : (
        <svg className='size-[17px]' viewBox='0 0 24 24' fill='none' stroke='currentColor' strokeWidth='2' strokeLinecap='round' strokeLinejoin='round' aria-hidden='true'>
          <path d='M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z' />
        </svg>
      )}
    </button>
  );
}
