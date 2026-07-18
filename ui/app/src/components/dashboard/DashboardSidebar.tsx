import { useState, useEffect, useRef, type ReactElement } from 'react';
import type { StringKey } from '@/i18n/strings';
import { useFocusTrap } from '@/hooks/useFocusTrap';

export type SectionId =
  | 'matches'
  | 'feed'
  | 'tools'
  | 'saved'
  | 'applications'
  | 'preferences'
  | 'billing'
  | 'settings'
  /** @deprecated routed to matches */
  | 'overview';

interface Section {
  id: SectionId;
  icon: ReactElement;
  labelKey: StringKey;
  badge?: number | null;
}

const SECTIONS: Section[] = [
  {
    id: 'matches',
    icon: (
      <svg
        className="h-5 w-5"
        fill="none"
        viewBox="0 0 24 24"
        strokeWidth={1.5}
        stroke="currentColor"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M21 8.25c0-2.485-2.099-4.5-4.688-4.5-1.935 0-3.597 1.126-4.312 2.733-.715-1.607-2.377-2.733-4.313-2.733C5.1 3.75 3 5.765 3 8.25c0 7.22 9 12 9 12s9-4.78 9-12Z"
        />
      </svg>
    ),
    labelKey: 'nav.matches',
  },
  {
    id: 'feed',
    icon: (
      <svg
        className="h-5 w-5"
        fill="none"
        viewBox="0 0 24 24"
        strokeWidth={1.5}
        stroke="currentColor"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M2.25 12.75V12A2.25 2.25 0 0 1 4.5 9.75h15A2.25 2.25 0 0 1 21.75 12v.75m-8.69-6.44-2.12-2.12a1.5 1.5 0 0 0-1.061-.44H4.5A2.25 2.25 0 0 0 2.25 6v12a2.25 2.25 0 0 0 2.25 2.25h15A2.25 2.25 0 0 0 21.75 18V9a2.25 2.25 0 0 0-2.25-2.25h-5.379a1.5 1.5 0 0 1-1.06-.44Z"
        />
      </svg>
    ),
    labelKey: 'nav.feed',
  },
  {
    id: 'tools',
    icon: (
      <svg
        className="h-5 w-5"
        fill="none"
        viewBox="0 0 24 24"
        strokeWidth={1.5}
        stroke="currentColor"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M11.42 15.17 17.25 21A2.652 2.652 0 0 0 21 17.25l-5.877-5.877M11.42 15.17l2.496-3.03c.317-.384.74-.626 1.208-.766M11.42 15.17l-4.655 5.653a2.548 2.548 0 1 1-3.586-3.586l6.837-5.63m5.108-.233c.55-.164 1.163-.188 1.743-.14a4.5 4.5 0 0 0 4.486-6.336l-3.276 3.277a3.004 3.004 0 0 1-2.25-2.25l3.276-3.276a4.5 4.5 0 0 0-6.336 4.486c.091 1.076-.071 2.264-.904 2.95l-.102.085m-1.745 1.437L5.909 7.5H4.5L2.25 3.75l1.5-1.5L7.5 4.5v1.409l4.26 4.26m-1.745 1.437 1.745-1.437m6.615 8.206L15.75 15.75M4.867 19.125h.008v.008h-.008v-.008Z"
        />
      </svg>
    ),
    labelKey: 'nav.tools',
  },
  {
    id: 'saved',
    icon: (
      <svg
        className="h-5 w-5"
        fill="none"
        viewBox="0 0 24 24"
        strokeWidth={1.5}
        stroke="currentColor"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M17.593 3.322c1.1.128 1.907 1.077 1.907 2.185V21L12 17.25 4.5 21V5.507c0-1.108.806-2.057 1.907-2.185a48.507 48.507 0 0 1 11.186 0Z"
        />
      </svg>
    ),
    labelKey: 'nav.saved',
  },
  {
    id: 'applications',
    icon: (
      <svg
        className="h-5 w-5"
        fill="none"
        viewBox="0 0 24 24"
        strokeWidth={1.5}
        stroke="currentColor"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z"
        />
      </svg>
    ),
    labelKey: 'nav.applications',
  },
  {
    id: 'preferences',
    icon: (
      <svg
        className="h-5 w-5"
        fill="none"
        viewBox="0 0 24 24"
        strokeWidth={1.5}
        stroke="currentColor"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M10.5 6h9.75M10.5 6a1.5 1.5 0 1 1-3 0m3 0a1.5 1.5 0 1 0-3 0M3.75 6H7.5m3 12h9.75m-9.75 0a1.5 1.5 0 0 1-3 0m3 0a1.5 1.5 0 0 0-3 0m-3.75 0H7.5m9-6h3.75m-3.75 0a1.5 1.5 0 0 1-3 0m3 0a1.5 1.5 0 0 0-3 0m-9.75 0h9.75"
        />
      </svg>
    ),
    labelKey: 'nav.preferences',
  },
  {
    id: 'billing',
    icon: (
      <svg
        className="h-5 w-5"
        fill="none"
        viewBox="0 0 24 24"
        strokeWidth={1.5}
        stroke="currentColor"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M2.25 8.25h19.5M2.25 9h19.5m-16.5 5.25h6m-6 2.25h3m-3.75 3h15a2.25 2.25 0 0 0 2.25-2.25V6.75A2.25 2.25 0 0 0 19.5 4.5h-15a2.25 2.25 0 0 0-2.25 2.25v10.5A2.25 2.25 0 0 0 4.5 19.5Z"
        />
      </svg>
    ),
    labelKey: 'nav.billing',
  },
  {
    id: 'settings',
    icon: (
      <svg
        className="h-5 w-5"
        fill="none"
        viewBox="0 0 24 24"
        strokeWidth={1.5}
        stroke="currentColor"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.325.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 0 1 1.37.49l1.296 2.247a1.125 1.125 0 0 1-.26 1.431l-1.003.827c-.293.241-.438.613-.43.992a7.723 7.723 0 0 1 0 .255c-.008.378.137.75.43.991l1.004.827c.424.35.534.955.26 1.43l-1.298 2.247a1.125 1.125 0 0 1-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.47 6.47 0 0 1-.22.128c-.331.183-.581.495-.644.869l-.213 1.281c-.09.543-.56.94-1.11.94h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 0 1-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 0 1-1.369-.49l-1.297-2.247a1.125 1.125 0 0 1 .26-1.431l1.004-.827c.292-.24.437-.613.43-.991a6.932 6.932 0 0 1 0-.255c.007-.38-.138-.751-.43-.992l-1.004-.827a1.125 1.125 0 0 1-.26-1.43l1.297-2.247a1.125 1.125 0 0 1 1.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.086.22-.128.332-.183.582-.495.644-.869l.214-1.28Z"
        />
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z"
        />
      </svg>
    ),
    labelKey: 'nav.settings',
  },
];

function SidebarNav({
  active,
  onNavigate,
  t,
  matchCount,
}: {
  active: SectionId;
  onNavigate: (id: SectionId) => void;
  t: (k: StringKey, fallback?: string) => string;
  matchCount?: number | null;
}) {
  const sections =
    matchCount != null
      ? SECTIONS.map((s) => ({ ...s, badge: s.id === 'matches' ? matchCount : null }))
      : SECTIONS;
  return (
    <nav className="space-y-1" aria-label="Dashboard sections">
      {sections.map((s) => {
        const isActive = s.id === active;
        return (
          <button
            key={s.id}
            type="button"
            onClick={() => onNavigate(s.id)}
            className={`flex min-h-[44px] w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm font-medium transition-all ${
              isActive
                ? 'bg-gradient-to-r from-navy-50 to-transparent text-navy-900 dark:from-navy-800 dark:to-transparent dark:text-white'
                : 'text-gray-600 hover:bg-gray-50 hover:text-gray-900 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white'
            }`}
          >
            <span
              className={
                isActive ? 'text-navy-700 dark:text-navy-300' : 'text-gray-400 dark:text-gray-500'
              }
            >
              {s.icon}
            </span>
            <span>{t(s.labelKey)}</span>
            {s.badge != null && (
              <span className="ml-auto inline-flex items-center rounded-full bg-accent-100 px-2 py-0.5 text-xs font-medium text-accent-700">
                {s.badge}
              </span>
            )}
          </button>
        );
      })}
    </nav>
  );
}

export function DashboardSidebar({
  active,
  onNavigate,
  t,
  matchCount,
}: {
  active: SectionId;
  onNavigate: (id: SectionId) => void;
  t: (k: StringKey, fallback?: string) => string;
  matchCount?: number | null;
}) {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const drawerRef = useRef<HTMLDivElement>(null);
  useFocusTrap(drawerRef, drawerOpen, () => setDrawerOpen(false));

  useEffect(() => {
    if (!drawerOpen) return;
    const close = (e: PointerEvent) => {
      if (drawerRef.current && !drawerRef.current.contains(e.target as Node)) {
        setDrawerOpen(false);
      }
    };
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && setDrawerOpen(false);
    document.addEventListener('pointerdown', close);
    document.addEventListener('keydown', esc);
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('pointerdown', close);
      document.removeEventListener('keydown', esc);
      document.body.style.overflow = '';
    };
  }, [drawerOpen]);

  const handleNav = (id: SectionId) => {
    onNavigate(id);
    setDrawerOpen(false);
  };

  return (
    <>
      {/* Mobile hamburger — visible below md */}
      <button
        type="button"
        className="flex items-center gap-2 rounded-md px-3 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-navy-800 md:hidden min-h-[44px]"
        aria-label="Open dashboard navigation"
        aria-expanded={drawerOpen}
        onClick={() => setDrawerOpen((o) => !o)}
      >
        <svg
          className="h-5 w-5"
          fill="none"
          viewBox="0 0 24 24"
          strokeWidth={1.5}
          stroke="currentColor"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5"
          />
        </svg>
        {t(
          active === 'matches'
            ? 'nav.matches'
            : active === 'feed'
              ? 'nav.feed'
              : active === 'tools'
                ? 'nav.tools'
                : active === 'saved'
                  ? 'nav.saved'
                  : active === 'applications'
                    ? 'nav.applications'
                    : active === 'preferences'
                      ? 'nav.preferences'
                      : active === 'billing'
                        ? 'nav.billing'
                        : 'nav.settings'
        )}
      </button>

      {/* Mobile drawer overlay */}
      {drawerOpen && <div className="fixed inset-0 z-50 bg-black/30 md:hidden" />}

      {/* Mobile drawer */}
      <div
        ref={drawerRef}
        className={`fixed left-0 top-0 z-50 h-full w-64 max-w-[85vw] transform bg-white shadow-xl transition-transform duration-200 ease-in-out md:hidden dark:bg-navy-900 ${
          drawerOpen ? 'translate-x-0' : '-translate-x-full'
        }`}
      >
        <div className="flex items-center justify-between border-b border-gray-100 px-4 py-4 dark:border-navy-700">
          <span className="text-sm font-semibold text-gray-900 dark:text-white">
            {t('nav.dashboard')}
          </span>
          <button
            type="button"
            className="flex h-10 w-10 items-center justify-center rounded-md text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:text-gray-500 dark:hover:bg-navy-800 dark:hover:text-gray-300"
            aria-label="Close dashboard navigation"
            onClick={() => setDrawerOpen(false)}
          >
            <svg
              className="h-5 w-5"
              fill="none"
              viewBox="0 0 24 24"
              strokeWidth={2}
              stroke="currentColor"
              aria-hidden="true"
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="px-3 py-4">
          <SidebarNav active={active} onNavigate={handleNav} t={t} matchCount={matchCount} />
        </div>
      </div>

      {/* Desktop sidebar — visible from md up */}
      <div className="hidden md:block">
        <div className="sticky top-[88px]">
          <SidebarNav active={active} onNavigate={handleNav} t={t} matchCount={matchCount} />
        </div>
      </div>
    </>
  );
}
