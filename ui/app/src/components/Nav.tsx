import { useState } from 'react';
import { StawiAuth } from './StawiAuth';

function MobileMenu({ open }: { open: boolean }) {
  if (!open) return null;
  return (
    <div className="border-t border-gray-100 bg-white px-4 pb-4 pt-2 md:hidden">
      <a
        href="/search/"
        className="flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50"
      >
        Find jobs
      </a>
      <a
        href="/jobs/"
        className="flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50"
      >
        Browse jobs
      </a>
      <a
        href="/categories/"
        className="flex rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50"
      >
        Categories
      </a>
      <a
        href="/pricing/"
        className="flex rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50"
      >
        Pricing
      </a>
      <a
        href="/about/"
        className="flex rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50"
      >
        About
      </a>
      <a href="/faq/" className="flex rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50">
        FAQ
      </a>
    </div>
  );
}

export default function Nav() {
  const [mobileOpen, setMobileOpen] = useState(false);

  return (
    <header
      className="sticky top-0 z-40 border-b border-gray-100 bg-white/95 shadow-sm backdrop-blur-xl"
      role="banner"
    >
      <div className="mx-auto flex h-[72px] max-w-7xl items-center justify-between gap-6 px-4 sm:px-6 lg:px-8">
        <a href="/" className="flex-shrink-0" aria-label="Stawi Jobs — Growing together">
          <img src="/images/logo.svg" alt="Stawi" height="40" className="h-10 w-auto" />
        </a>

        <nav className="hidden items-center gap-2 md:flex" aria-label="Main navigation">
          <a
            href="/search/"
            className="rounded-md px-3.5 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900"
          >
            Find jobs
          </a>
          <a
            href="/jobs/"
            className="rounded-md px-3.5 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900"
          >
            Browse jobs
          </a>
          <a
            href="/categories/"
            className="rounded-md px-3.5 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900"
          >
            Categories
          </a>
          <a
            href="/pricing/"
            className="rounded-md px-3.5 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900"
          >
            Pricing
          </a>
          <a
            href="/about/"
            className="rounded-md px-3.5 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900"
          >
            About
          </a>
        </nav>

        <div className="flex items-center gap-2">
          <StawiAuth />

          <button
            type="button"
            className="flex min-h-[44px] min-w-[44px] items-center justify-center rounded-md p-2 text-gray-600 hover:bg-gray-100 hover:text-navy-900 md:hidden"
            aria-label={mobileOpen ? 'Close menu' : 'Open menu'}
            aria-expanded={mobileOpen}
            onClick={() => setMobileOpen((o) => !o)}
          >
            {mobileOpen ? (
              <svg
                className="h-5 w-5"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
              </svg>
            ) : (
              <svg
                className="h-5 w-5"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5"
                />
              </svg>
            )}
          </button>
        </div>
      </div>

      <MobileMenu open={mobileOpen} />
    </header>
  );
}
