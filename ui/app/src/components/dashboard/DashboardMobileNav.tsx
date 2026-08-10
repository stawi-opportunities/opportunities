import { useEffect, type ReactElement } from 'react';
import type { StringKey } from '@/i18n/strings';
import type { SectionId } from './DashboardSidebar';

/**
 * Mobile navigation drawer (replaces bottom tabs).
 * Desktop keeps the left sidebar; this is md:hidden only.
 */
const ITEMS: {
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
  },
  {
    id: 'saved',
    labelKey: 'nav.saved',
    short: 'Saved',
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
  },
  {
    id: 'applications',
    labelKey: 'nav.applications',
    short: 'Applications',
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
  },
  {
    id: 'cv',
    labelKey: 'nav.cv',
    short: 'CV',
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
  },
  {
    id: 'settings',
    labelKey: 'nav.settings',
    short: 'Settings',
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
  },
];

export function DashboardMobileNav({
  open,
  onClose,
  active,
  onNavigate,
  t,
  matchCount,
}: {
  open: boolean;
  onClose: () => void;
  active: SectionId;
  onNavigate: (id: SectionId) => void;
  t: (k: StringKey, fallback?: string) => string;
  matchCount?: number | null;
}) {
  // Lock body scroll while the drawer is open.
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  // Escape closes.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 md:hidden" role="dialog" aria-modal="true" aria-label="Menu">
      <button
        type="button"
        className="absolute inset-0 bg-navy-950/40 backdrop-blur-[2px]"
        aria-label="Close menu"
        onClick={onClose}
      />
      <nav
        className="absolute inset-y-0 left-0 flex w-[min(20rem,88vw)] flex-col bg-surface shadow-[var(--shadow-lift)] animate-slide-down"
        style={{ animationName: 'none' }}
      >
        <div className="flex items-center justify-between border-b border-muted px-4 py-3">
          <p className="text-sm font-semibold text-main">Menu</p>
          <button
            type="button"
            onClick={onClose}
            className="inline-flex h-10 w-10 items-center justify-center rounded-lg text-secondary hover:bg-surface-hover hover:text-main"
            aria-label="Close menu"
          >
            <svg className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor" aria-hidden>
              <path d="M6.28 5.22a.75.75 0 00-1.06 1.06L8.94 10l-3.72 3.72a.75.75 0 101.06 1.06L10 11.06l3.72 3.72a.75.75 0 101.06-1.06L11.06 10l3.72-3.72a.75.75 0 00-1.06-1.06L10 8.94 6.28 5.22z" />
            </svg>
          </button>
        </div>
        <ul className="flex-1 space-y-0.5 overflow-y-auto p-3">
          {ITEMS.map((item) => {
            const isActive = item.id === active;
            const badge =
              item.id === 'matches' && matchCount != null && matchCount > 0 ? matchCount : null;
            return (
              <li key={item.id}>
                <button
                  type="button"
                  onClick={() => {
                    onNavigate(item.id);
                    onClose();
                  }}
                  aria-current={isActive ? 'page' : undefined}
                  className={`flex min-h-[48px] w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm font-medium transition-colors ${
                    isActive
                      ? 'bg-accent-500/10 text-main ring-1 ring-inset ring-accent-500/25'
                      : 'text-secondary hover:bg-surface-hover hover:text-main'
                  }`}
                >
                  <span
                    className={isActive ? 'text-accent-600 dark:text-accent-400' : 'text-secondary'}
                  >
                    {item.icon}
                  </span>
                  <span className="truncate">{t(item.labelKey, item.short)}</span>
                  {badge != null && (
                    <span className="ml-auto inline-flex min-w-[1.25rem] items-center justify-center rounded-full bg-accent-600 px-1.5 py-0.5 text-[11px] font-semibold tabular-nums text-white">
                      {badge > 99 ? '99+' : badge}
                    </span>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
        {/* Profile lives in the site nav avatar — never duplicate it in the drawer. */}
      </nav>
    </div>
  );
}
