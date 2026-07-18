import { useState } from 'react';
import { StawiAuth } from './StawiAuth';

function MobileMenu({ open }: { open: boolean }) {
  if (!open) return null;
  return (
    <div className="border-t border-gray-100 bg-white px-4 pb-3 pt-2 md:hidden">
      <a
        href="/search/"
        className="block rounded-md px-3 py-2 text-sm text-gray-700 hover:bg-gray-50"
      >
        Jobs
      </a>
      <a
        href="/pricing/"
        className="block rounded-md px-3 py-2 text-sm text-gray-700 hover:bg-gray-50"
      >
        Pricing
      </a>
    </div>
  );
}

export default function Nav() {
  const [mobileOpen, setMobileOpen] = useState(false);

  return (
    <header
      className="sticky top-0 z-40 border-b border-gray-100 bg-white/95 backdrop-blur-xl"
      role="banner"
    >
      <div className="mx-auto flex h-14 max-w-7xl items-center justify-between gap-4 px-4 sm:px-6 lg:px-8">
        <a href="/" className="flex-shrink-0" aria-label="Stawi Jobs">
          <img src="/images/logo.svg" alt="Stawi" height="32" className="h-8 w-auto" />
        </a>

        <nav className="hidden items-center gap-1 md:flex" aria-label="Main">
          <a
            href="/search/"
            className="rounded-md px-3 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 hover:text-navy-900"
          >
            Jobs
          </a>
          <a
            href="/pricing/"
            className="rounded-md px-3 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100 hover:text-navy-900"
          >
            Pricing
          </a>
        </nav>

        <div className="flex items-center gap-1">
          <StawiAuth />
          <button
            type="button"
            className="flex h-10 w-10 items-center justify-center rounded-md text-gray-600 hover:bg-gray-100 md:hidden"
            aria-label={mobileOpen ? 'Close menu' : 'Open menu'}
            aria-expanded={mobileOpen}
            onClick={() => setMobileOpen((o) => !o)}
          >
            <svg
              className="h-5 w-5"
              fill="none"
              viewBox="0 0 24 24"
              strokeWidth={2}
              stroke="currentColor"
            >
              {mobileOpen ? (
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
              ) : (
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5"
                />
              )}
            </svg>
          </button>
        </div>
      </div>
      <MobileMenu open={mobileOpen} />
    </header>
  );
}
