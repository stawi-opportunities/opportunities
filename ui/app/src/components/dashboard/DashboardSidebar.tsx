import type { ReactElement } from 'react';
import type { StringKey } from '@/i18n/strings';

export type SectionId =
  | 'matches'
  | 'cv'
  | 'saved'
  | 'applications'
  | 'settings'
  /** @deprecated → matches */
  | 'feed'
  | 'tools'
  | 'preferences'
  | 'billing'
  | 'overview';

interface Section {
  id: SectionId | 'jobs';
  icon: ReactElement;
  labelKey: StringKey;
  badge?: number | null;
  /** When set, renders as a link out of the dashboard (e.g. full job catalog). */
  href?: string;
}

/** Matches → All jobs → Saved → Applications → CV → Settings */
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
    id: 'jobs',
    href: '/jobs/',
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
          d="M20.25 14.15v4.25c0 1.094-.787 2.036-1.872 2.18-2.087.277-4.216.42-6.378.42s-4.291-.143-6.378-.42c-1.085-.144-1.872-1.086-1.872-2.18v-4.25m16.5 0a2.18 2.18 0 0 0 .75-1.661V8.706c0-1.081-.768-2.015-1.837-2.175a48.114 48.114 0 0 0-3.413-.387m4.5 2.652V7.204a2.25 2.25 0 0 0-1.88-2.222c-1.392-.22-2.824-.36-4.28-.415m-8.64 0c-1.456.055-2.888.196-4.28.415A2.25 2.25 0 0 0 3 7.204v1.286c0 .224.033.444.096.652m7.5 0a48.667 48.667 0 0 0-7.5 0m7.5 0V5.232c0-.41.328-.746.736-.79A48.11 48.11 0 0 1 12 4.5c1.255 0 2.492.066 3.714.194a.75.75 0 0 1 .736.79V8.5m-7.5 0h7.5"
        />
      </svg>
    ),
    labelKey: 'nav.allJobs',
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
    id: 'cv',
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
          d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z"
        />
      </svg>
    ),
    labelKey: 'nav.cv',
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

/**
 * Desktop sidebar only. Mobile uses DashboardMobileNav (drawer).
 * Account avatar lives in the site Nav profile widget — not here.
 */
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
  const sections =
    matchCount != null
      ? SECTIONS.map((s) => ({ ...s, badge: s.id === 'matches' ? matchCount : null }))
      : SECTIONS;

  const itemClass = (isActive: boolean) =>
    `flex min-h-[44px] w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm font-medium transition-colors ${
      isActive
        ? 'bg-accent-500/10 text-main ring-1 ring-inset ring-accent-500/25'
        : 'text-secondary hover:bg-surface-hover hover:text-main'
    }`;

  return (
    <div className="sticky top-[88px]">
      <nav className="space-y-0.5" aria-label="Dashboard sections">
        {sections.map((s) => {
          const isActive = s.id === active;
          const icon = (
            <span className={isActive ? 'text-accent-600 dark:text-accent-400' : 'text-secondary'}>
              {s.icon}
            </span>
          );
          const label = <span className="truncate">{t(s.labelKey)}</span>;
          const badge =
            s.badge != null && s.badge > 0 ? (
              <span className="ml-auto inline-flex min-w-[1.25rem] items-center justify-center rounded-full bg-accent-600 px-1.5 py-0.5 text-[11px] font-semibold tabular-nums text-white">
                {s.badge > 99 ? '99+' : s.badge}
              </span>
            ) : null;

          if (s.href) {
            return (
              <a key={s.id} href={s.href} className={itemClass(false)}>
                {icon}
                {label}
                <span className="ml-auto text-secondary/60" aria-hidden="true">
                  <svg className="h-3.5 w-3.5" viewBox="0 0 20 20" fill="currentColor">
                    <path
                      fillRule="evenodd"
                      d="M5.22 14.78a.75.75 0 001.06 0l7.22-7.22v5.69a.75.75 0 001.5 0v-7.5a.75.75 0 00-.75-.75h-7.5a.75.75 0 000 1.5h5.69l-7.22 7.22a.75.75 0 000 1.06z"
                      clipRule="evenodd"
                    />
                  </svg>
                </span>
              </a>
            );
          }

          return (
            <button
              key={s.id}
              type="button"
              onClick={() => onNavigate(s.id as SectionId)}
              aria-current={isActive ? 'page' : undefined}
              className={itemClass(isActive)}
            >
              {icon}
              {label}
              {badge}
            </button>
          );
        })}
      </nav>
    </div>
  );
}
