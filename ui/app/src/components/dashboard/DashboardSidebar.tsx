import { useState, useEffect, useRef, type ReactElement } from 'react';
import type { StringKey } from '@/i18n/strings';
import { useFocusTrap } from '@/hooks/useFocusTrap';

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
  id: SectionId;
  icon: ReactElement;
  labelKey: StringKey;
  badge?: number | null;
}

/** Matches → Saved → Applications → CV → Settings */
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

const LABEL_FOR: Record<string, StringKey> = {
  matches: 'nav.matches',
  cv: 'nav.cv',
  saved: 'nav.saved',
  applications: 'nav.applications',
  settings: 'nav.settings',
};

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
                ? 'border border-accent-500/30 bg-accent-500/10 text-white'
                : 'text-secondary hover:bg-surface-hover hover:text-main'
            }`}
          >
            <span className={isActive ? 'text-accent-400' : 'text-secondary/60'}>{s.icon}</span>
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

  const mobileLabelKey = LABEL_FOR[active] ?? 'nav.matches';

  return (
    <>
      <button
        type="button"
        className="flex min-h-[44px] items-center gap-2 rounded-md px-3 py-2.5 text-sm font-medium text-main hover:bg-surface-hover md:hidden"
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
        {t(mobileLabelKey)}
      </button>

      {drawerOpen && <div className="fixed inset-0 z-50 bg-black/30 md:hidden" />}

      <div
        ref={drawerRef}
        className={`fixed left-0 top-0 z-50 h-full w-64 max-w-[85vw] transform border-r border-muted bg-nav-bg shadow-xl transition-transform duration-200 ease-in-out md:hidden ${
          drawerOpen ? 'translate-x-0' : '-translate-x-full'
        }`}
      >
        <div className="flex items-center justify-between border-b border-muted px-4 py-4">
          <span className="text-sm font-semibold text-main">{t('nav.dashboard')}</span>
          <button
            type="button"
            className="flex h-10 w-10 items-center justify-center rounded-md text-secondary hover:bg-surface-hover hover:text-main"
            aria-label="Close dashboard navigation"
            onClick={() => setDrawerOpen(false)}
          >
            <svg
              className="h-5 w-5"
              fill="none"
              viewBox="0 0 24 24"
              strokeWidth={2}
              stroke="currentColor"
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="px-3 py-4">
          <SidebarNav active={active} onNavigate={handleNav} t={t} matchCount={matchCount} />
        </div>
      </div>

      <div className="hidden md:block">
        <div className="sticky top-[88px]">
          <SidebarNav active={active} onNavigate={onNavigate} t={t} matchCount={matchCount} />
        </div>
      </div>
    </>
  );
}
