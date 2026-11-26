import { Link } from '@tanstack/react-router';

export function NotFound() {
  return (
    <div className='flex min-h-[60vh] flex-col items-center justify-center gap-4 text-center'>
      <h1>404 — Page not found</h1>
      <p>That page doesn’t exist (yet).</p>
      <Link to='/'>← Back to the docs</Link>
    </div>
  );
}
