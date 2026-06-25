import { useState, useRef, useEffect } from 'react';
import { StawiAuth } from './StawiAuth';
import { LanguageSwitcher } from './LanguageSwitcher';
import { useAuth } from '@/providers/AuthProvider';
import { Icon } from './ui/Icon';

const browseItems = [
  { href: '/jobs/', icon: 'briefcase' as const, label: 'Jobs', sub: 'Full-time, remote & more' },
  {
    href: '/scholarships/',
    icon: 'graduation' as const,
    label: 'Scholarships',
    sub: 'Grants & bursaries',
  },
  { href: '/tenders/', icon: 'clipboard' as const, label: 'Tenders', sub: 'RFPs & procurement' },
  { href: '/deals/', icon: 'tag' as const, label: 'Deals', sub: 'Curated discounts' },
  { href: '/funding/', icon: 'money' as const, label: 'Funding', sub: 'Grants & investment' },
];

function BrowseDropdown() {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false);
    document.addEventListener('mousedown', close);
    document.addEventListener('keydown', esc);
    return () => {
      document.removeEventListener('mousedown', close);
      document.removeEventListener('keydown', esc);
    };
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        aria-haspopup="true"
        className="flex items-center gap-1 rounded-md px-3 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white"
      >
        Browse
        <svg
          className={`h-4 w-4 transition-transform duration-150 ${open ? 'rotate-180' : ''}`}
          fill="none"
          viewBox="0 0 24 24"
          strokeWidth={2}
          stroke="currentColor"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="m19 9-7 7-7-7" />
        </svg>
      </button>

      {open && (
        <div className="absolute left-0 top-full z-50 mt-1 w-52 rounded-xl border border-gray-100 bg-white shadow-lg ring-1 ring-black/5 dark:border-navy-700 dark:bg-navy-900">
          <div className="p-1.5">
            {browseItems.map(({ href, icon, label, sub }) => (
              <a
                key={href}
                href={href}
                onClick={() => setOpen(false)}
                className="flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm text-gray-700 hover:bg-gray-50 hover:text-navy-900 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white"
              >
                <span className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md bg-gray-100 text-gray-600 dark:bg-navy-800 dark:text-gray-400">
                  <Icon name={icon} size={16} />
                </span>
                <div>
                  <div className="font-medium">{label}</div>
                  <div className="text-xs text-gray-500 dark:text-gray-400">{sub}</div>
                </div>
              </a>
            ))}
          </div>
          <div className="border-t border-gray-100 px-3 py-2 dark:border-navy-700">
            <a
              href="/search/"
              className="flex items-center gap-1.5 text-xs font-medium text-navy-700 hover:text-navy-900 dark:text-navy-300 dark:hover:text-white"
            >
              <svg
                className="h-3.5 w-3.5"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <circle cx="11" cy="11" r="8" />
                <path strokeLinecap="round" strokeLinejoin="round" d="m21 21-4.35-4.35" />
              </svg>
              Advanced search
            </a>
          </div>
        </div>
      )}
    </div>
  );
}

function MobileMenu({ open, isAuth }: { open: boolean; isAuth: boolean }) {
  if (!open) return null;
  return (
    <div className="border-t border-gray-100 bg-white px-4 pb-4 pt-2 md:hidden animate-slide-down dark:border-navy-700 dark:bg-navy-900">
      {isAuth && (
        <a
          href="/dashboard/"
          className="mb-2 flex items-center gap-2 rounded-lg bg-navy-50 px-3 py-2 text-sm font-medium text-navy-900 hover:bg-navy-100 dark:text-white"
        >
          <svg
            className="h-4 w-4"
            fill="none"
            viewBox="0 0 24 24"
            strokeWidth={2}
            stroke="currentColor"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="M3.75 6A2.25 2.25 0 0 1 6 3.75h2.25A2.25 2.25 0 0 1 10.5 6v2.25a2.25 2.25 0 0 1-2.25 2.25H6a2.25 2.25 0 0 1-2.25-2.25V6ZM3.75 15.75A2.25 2.25 0 0 1 6 13.5h2.25a2.25 2.25 0 0 1 2.25 2.25V18a2.25 2.25 0 0 1-2.25 2.25H6A2.25 2.25 0 0 1 3.75 18v-2.25ZM13.5 6a2.25 2.25 0 0 1 2.25-2.25H18A2.25 2.25 0 0 1 20.25 6v2.25A2.25 2.25 0 0 1 18 10.5h-2.25a2.25 2.25 0 0 1-2.25-2.25V6ZM13.5 15.75a2.25 2.25 0 0 1 2.25-2.25H18a2.25 2.25 0 0 1 2.25 2.25V18A2.25 2.25 0 0 1 18 20.25h-2.25A2.25 2.25 0 0 1 13.5 18v-2.25Z"
            />
          </svg>
          Dashboard
        </a>
      )}
      <p className="mb-1 px-3 text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
        Browse
      </p>
      {browseItems.map(({ href, icon, label }) => (
        <a
          key={href}
          href={href}
          className="flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-navy-800"
        >
          <Icon name={icon} size={16} className="text-gray-500 dark:text-gray-400 flex-shrink-0" />
          {label}
        </a>
      ))}
      <div className="my-2 border-t border-gray-100 dark:border-navy-700" />
      <a
        href="/categories/"
        className="flex rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-navy-800"
      >
        Categories
      </a>
      <a
        href="/pricing/"
        className="flex rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-navy-800"
      >
        Pricing
      </a>
      <a
        href="/about/"
        className="flex rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-navy-800"
      >
        About
      </a>
      <a
        href="/faq/"
        className="flex rounded-lg px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-navy-800"
      >
        FAQ
      </a>
    </div>
  );
}

export default function Nav() {
  const { state } = useAuth();
  const isAuth = state === 'authenticated';
  const [mobileOpen, setMobileOpen] = useState(false);

  return (
    <header
      className="sticky top-0 z-40 border-b border-gray-100 bg-white/95 shadow-sm backdrop-blur-xl dark:border-navy-700 dark:bg-navy-900/95"
      role="banner"
    >
      <div className="mx-auto flex h-[72px] max-w-7xl items-center justify-between gap-6 px-4 sm:px-6 lg:px-8">
        {/* Logo */}
        <a href="/" className="flex-shrink-0" aria-label={'Stawi \u2014 Growing together'}>
          <img src="/images/logo.svg" alt="Stawi" height="40" className="h-10 w-auto" />
        </a>

        {/* Desktop nav */}
        <nav className="hidden items-center gap-2 md:flex" aria-label="Main navigation">
          <BrowseDropdown />
          {isAuth && (
            <a
              href="/dashboard/"
              className="inline-flex items-center gap-1.5 rounded-md px-3.5 py-2 text-sm font-medium text-navy-900 transition-colors hover:bg-navy-50 dark:text-white"
            >
              <svg
                className="h-4 w-4"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M3.75 6A2.25 2.25 0 0 1 6 3.75h2.25A2.25 2.25 0 0 1 10.5 6v2.25a2.25 2.25 0 0 1-2.25 2.25H6a2.25 2.25 0 0 1-2.25-2.25V6ZM3.75 15.75A2.25 2.25 0 0 1 6 13.5h2.25a2.25 2.25 0 0 1 2.25 2.25V18a2.25 2.25 0 0 1-2.25 2.25H6A2.25 2.25 0 0 1 3.75 18v-2.25ZM13.5 6a2.25 2.25 0 0 1 2.25-2.25H18A2.25 2.25 0 0 1 20.25 6v2.25A2.25 2.25 0 0 1 18 10.5h-2.25a2.25 2.25 0 0 1-2.25-2.25V6ZM13.5 15.75a2.25 2.25 0 0 1 2.25-2.25H18a2.25 2.25 0 0 1 2.25 2.25V18A2.25 2.25 0 0 1 18 20.25h-2.25A2.25 2.25 0 0 1 13.5 18v-2.25Z"
                />
              </svg>
              Dashboard
            </a>
          )}
          <a
            href="/categories/"
            className="rounded-md px-3.5 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white"
          >
            Categories
          </a>
          <a
            href="/pricing/"
            className="rounded-md px-3.5 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white"
          >
            Pricing
          </a>
          <a
            href="/about/"
            className="rounded-md px-3.5 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white"
          >
            About
          </a>
          <a
            href="/faq/"
            className="rounded-md px-3.5 py-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-navy-900 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white"
          >
            FAQ
          </a>
        </nav>

        {/* Right side */}
        <div className="flex items-center gap-2">
          <LanguageSwitcher />
          <StawiAuth />

          {/* Mobile hamburger */}
          <button
            type="button"
            className="flex items-center rounded-md p-2 text-gray-600 hover:bg-gray-100 hover:text-navy-900 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white md:hidden"
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

      <MobileMenu open={mobileOpen} isAuth={isAuth} />
    </header>
  );
}
