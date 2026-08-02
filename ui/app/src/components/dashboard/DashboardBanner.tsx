import { useState, useEffect } from 'react';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import { useAuth } from '@/providers/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';

const VISIT_KEY = 'stawi.welcome_visits';
const MAX_VISITS = 3;

function getVisitCount(): number {
  try {
    return Number(localStorage.getItem(VISIT_KEY)) || 0;
  } catch {
    return MAX_VISITS;
  }
}

function incrementVisit() {
  try {
    localStorage.setItem(VISIT_KEY, String(getVisitCount() + 1));
  } catch {
    // private mode
  }
}

export function DashboardBanner({ onStartTour }: { onStartTour?: () => void }) {
  const { t } = useI18n();
  const { runtime } = useAuth();
  const { data: profile, isLoading } = useCandidateProfile();
  const [visible, setVisible] = useState(false);
  const [initial, setInitial] = useState<string | null>(null);

  useEffect(() => {
    if (getVisitCount() < MAX_VISITS) {
      setVisible(true);
      incrementVisit();
    }
  }, []);

  useEffect(() => {
    runtime
      .getClaims()
      .then((claims) => {
        const name = String(claims.name ?? claims.preferred_username ?? '');
        setInitial(name ? name.charAt(0).toUpperCase() : null);
      })
      .catch(() => setInitial(null));
  }, [runtime]);

  if (!visible || isLoading) return null;

  const title = profile?.current_title;

  return (
    <div className="relative overflow-hidden rounded-xl border border-accent-500/20 bg-gradient-to-r from-accent-500/10 via-surface to-surface p-5 shadow-sm sm:p-6">
      <button
        type="button"
        onClick={() => setVisible(false)}
        className="absolute right-3 top-3 flex h-10 w-10 items-center justify-center rounded text-secondary transition-colors hover:bg-surface-hover hover:text-main"
        aria-label={t('cta.dismiss')}
      >
        <svg
          className="h-5 w-5"
          fill="none"
          viewBox="0 0 24 24"
          strokeWidth={1.5}
          stroke="currentColor"
          aria-hidden="true"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>

      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-xl font-semibold tracking-tight text-main">
            {t('dash.welcomeTitle')}
          </h2>
          <p className="mt-1 text-sm text-secondary">
            {title && <span>{title} &middot; </span>}
            <button
              type="button"
              onClick={onStartTour}
              className="font-medium text-accent-400 underline underline-offset-2 hover:text-accent-300"
            >
              {t('dash.welcomeTour')}
            </button>
          </p>
        </div>

        <div className="flex shrink-0 items-center gap-3">
          {initial ? (
            <div className="flex h-10 w-10 items-center justify-center rounded-full bg-accent-500/15 text-sm font-bold text-accent-400 ring-1 ring-accent-500/30">
              {initial}
            </div>
          ) : (
            <div className="flex h-10 w-10 items-center justify-center rounded-full bg-surface-hover text-secondary ring-1 ring-muted-strong">
              <svg
                className="h-5 w-5"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={1.5}
                stroke="currentColor"
                aria-hidden="true"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M15.75 6a3.75 3.75 0 1 1-7.5 0 3.75 3.75 0 0 1 7.5 0ZM4.501 20.118a7.5 7.5 0 0 1 14.998 0A17.933 17.933 0 0 1 12 21.75c-2.676 0-5.216-.584-7.499-1.632Z"
                />
              </svg>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
