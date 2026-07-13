import { useState, useEffect } from 'react';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
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
  const { data: profile, isLoading } = useCandidateProfile();
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    if (getVisitCount() < MAX_VISITS) {
      setVisible(true);
      incrementVisit();
    }
  }, []);

  if (!visible || isLoading) return null;

  const title = profile?.current_title;

  return (
    <div className="relative overflow-hidden rounded-xl bg-gradient-to-r from-navy-50 to-blue-50 p-5 shadow-sm ring-1 ring-navy-100 dark:from-navy-800 dark:to-navy-900 dark:ring-navy-700 sm:p-6">
      <button
        type="button"
        onClick={() => setVisible(false)}
        className="absolute right-3 top-3 rounded p-1 text-gray-400 transition-colors hover:bg-white/50 hover:text-gray-600 dark:hover:bg-navy-700 dark:hover:text-gray-300"
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
          <h2 className="text-xl font-semibold tracking-tight text-gray-900 dark:text-white">
            {t('dash.welcomeTitle')}
          </h2>
          <p className="mt-1 text-sm text-gray-600 dark:text-gray-300">
            {title || (
              <>
                {t('dash.welcomeBody')}{' '}
                <button
                  type="button"
                  onClick={onStartTour}
                  className="font-medium text-navy-700 underline underline-offset-2 hover:text-navy-900 dark:text-navy-300 dark:hover:text-white"
                >
                  {t('dash.welcomeTour')}
                </button>
              </>
            )}
          </p>
        </div>

        <div className="flex shrink-0 items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-full bg-white text-sm font-bold text-navy-800 shadow-sm ring-2 ring-navy-200 dark:bg-navy-700 dark:text-white dark:ring-navy-600">
            {'S'}
          </div>
        </div>
      </div>
    </div>
  );
}
