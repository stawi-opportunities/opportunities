import { useEffect, useState } from 'react';
import type { StringKey } from '@/i18n/strings';

const STORAGE_KEY = 'stawi.welcome_visits';
const MAX_VISITS = 3;

function getVisitCount(): number {
  try {
    return Number(localStorage.getItem(STORAGE_KEY)) || 0;
  } catch {
    return MAX_VISITS;
  }
}

function setVisitCount(n: number) {
  try {
    localStorage.setItem(STORAGE_KEY, String(n));
  } catch {
    /* private mode */
  }
}

export function WelcomeBanner({ t }: { t: (k: StringKey, fallback?: string) => string }) {
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    const count = getVisitCount();
    if (count < MAX_VISITS) {
      setVisitCount(count + 1);
      setVisible(true);
    }
  }, []);

  if (!visible) return null;

  return (
    <div className="relative rounded-lg border border-blue-200 bg-blue-50 p-4 sm:p-6">
      <button
        type="button"
        onClick={() => setVisible(false)}
        className="absolute right-2 top-2 rounded p-1 text-blue-500 hover:bg-blue-100 hover:text-blue-700"
        aria-label={t('dash.welcomeDismiss')}
      >
        <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth="2"
            d="M6 18L18 6M6 6l12 12"
          />
        </svg>
      </button>
      <h2 className="text-lg font-semibold text-blue-900">{t('dash.welcomeTitle')}</h2>
      <p className="mt-1 text-sm text-blue-800">{t('dash.welcomeBody')}</p>
      <p className="mt-3 text-sm font-medium text-accent-600 hover:text-accent-700">
        {t('dash.welcomeTour')}
      </p>
    </div>
  );
}
