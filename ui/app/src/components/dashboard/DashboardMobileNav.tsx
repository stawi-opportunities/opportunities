import type { ReactElement } from 'react';
import type { StringKey } from '@/i18n/strings';
import type { SectionId } from './DashboardSidebar';

/**
 * Primary mobile navigation: fixed bottom tab bar (thumb-zone).
 * Desktop keeps the sidebar; this is md:hidden only.
 */
const TABS: {
  id: SectionId;
  labelKey: StringKey;
  short: string;
  icon: ReactElement;
}[] = [
  {
    id: 'matches',
    labelKey: 'nav.matches',
    short: 'Matches',
    icon: (
      <svg
        className="h-6 w-6"
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
  },
  {
    id: 'saved',
    labelKey: 'nav.saved',
    short: 'Saved',
    icon: (
      <svg
        className="h-6 w-6"
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
  },
  {
    id: 'applications',
    labelKey: 'nav.applications',
    short: 'Apps',
    icon: (
      <svg
        className="h-6 w-6"
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
  },
  {
    id: 'cv',
    labelKey: 'nav.cv',
    short: 'CV',
    icon: (
      <svg
        className="h-6 w-6"
        fill="none"
        viewBox="0 0 24 24"
        strokeWidth={1.5}
        stroke="currentColor"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z"
        />
      </svg>
    ),
  },
  {
    id: 'settings',
    labelKey: 'nav.settings',
    short: 'Settings',
    icon: (
      <svg
        className="h-6 w-6"
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
  },
];

export function DashboardMobileNav({
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
  return (
    <nav
      className="fixed inset-x-0 bottom-0 z-40 border-t border-muted bg-nav-bg/95 pb-[env(safe-area-inset-bottom)] backdrop-blur-md md:hidden"
      style={{ boxShadow: '0 -1px 0 rgb(var(--color-border) / 0.8)' }}
      aria-label="Primary"
    >
      <ul className="mx-auto flex max-w-lg items-stretch justify-between px-1 pt-0.5">
        {TABS.map((tab) => {
          const isActive = tab.id === active;
          const badge =
            tab.id === 'matches' && matchCount != null && matchCount > 0 ? matchCount : null;
          return (
            <li key={tab.id} className="min-w-0 flex-1">
              <button
                type="button"
                onClick={() => onNavigate(tab.id)}
                aria-current={isActive ? 'page' : undefined}
                className={`relative flex w-full flex-col items-center justify-center gap-0.5 px-1 py-2 text-[10px] font-medium leading-tight transition-colors sm:text-xs ${
                  isActive ? 'text-accent-700 dark:text-accent-400' : 'text-secondary'
                }`}
              >
                <span
                  className={`relative flex h-8 w-8 items-center justify-center rounded-lg ${
                    isActive ? 'bg-accent-500/12' : ''
                  }`}
                >
                  {tab.icon}
                  {badge != null && (
                    <span className="absolute -right-1 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-accent-600 px-1 text-[9px] font-bold text-white">
                      {badge > 99 ? '99+' : badge}
                    </span>
                  )}
                </span>
                <span className="max-w-full truncate">{t(tab.labelKey, tab.short)}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
