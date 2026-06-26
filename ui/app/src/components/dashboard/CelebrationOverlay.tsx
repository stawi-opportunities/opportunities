import { useEffect } from 'react';
import { Button } from '@/components/ui/Button';
import type { StringKey } from '@/i18n/strings';

interface Props {
  t: (k: StringKey, fallback?: string) => string;
  onDismiss: () => void;
}

const CONFETTI_COLORS = ['#10b981', '#f59e0b', '#3b82f6', '#8b5cf6', '#ef4444'];

export function CelebrationOverlay({ t, onDismiss }: Props) {
  useEffect(() => {
    const timer = setTimeout(onDismiss, 5000);
    return () => clearTimeout(timer);
  }, [onDismiss]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/20 p-4">
      <div className="relative max-w-md rounded-xl bg-white p-6 text-center shadow-2xl animate-fade-in dark:bg-navy-900">
        {/* Confetti dots */}
        <div
          className="pointer-events-none absolute inset-0 overflow-hidden rounded-xl"
          aria-hidden
        >
          {Array.from({ length: 20 }).map((_, i) => (
            <div
              key={i}
              className="absolute h-2 w-2 animate-confetti rounded-full"
              style={{
                left: `${10 + Math.random() * 80}%`,
                top: '-4px',
                animationDelay: `${Math.random() * 0.5}s`,
                backgroundColor: CONFETTI_COLORS[i % CONFETTI_COLORS.length],
              }}
            />
          ))}
        </div>

        <div className="relative">
          <div className="mx-auto flex h-16 w-16 items-center justify-center rounded-full bg-emerald-100 dark:bg-emerald-900/30">
            <svg
              className="h-8 w-8 text-emerald-600 dark:text-emerald-400"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2.5"
                d="M5 13l4 4L19 7"
              />
            </svg>
          </div>
          <h2 className="mt-4 text-xl font-bold text-gray-900 dark:text-white">
            {t('dash.celebrationTitle')}
          </h2>
          <p className="mt-2 text-sm text-gray-600 dark:text-gray-400">
            {t('dash.celebrationBody')}
          </p>
          <ul className="mt-4 space-y-2 text-left text-sm text-gray-700 dark:text-gray-300">
            <li className="flex items-center gap-2">
              <svg
                className="h-4 w-4 shrink-0 text-emerald-500"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth="2"
                  d="M5 13l4 4L19 7"
                />
              </svg>
              {t('dash.celebrationStep1')}
            </li>
            <li className="flex items-center gap-2">
              <svg
                className="h-4 w-4 shrink-0 text-emerald-500"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth="2"
                  d="M5 13l4 4L19 7"
                />
              </svg>
              {t('dash.celebrationStep2')}
            </li>
            <li className="flex items-center gap-2">
              <svg
                className="h-4 w-4 shrink-0 text-emerald-500"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth="2"
                  d="M5 13l4 4L19 7"
                />
              </svg>
              {t('dash.celebrationStep3')}
            </li>
          </ul>
          <Button variant="primary" size="md" type="button" onClick={onDismiss} className="w-full">
            {t('dash.celebrationDismiss')}
          </Button>
        </div>
      </div>
    </div>
  );
}
