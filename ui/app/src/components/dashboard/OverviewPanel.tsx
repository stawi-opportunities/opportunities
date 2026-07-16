import { useI18n } from '@/i18n/I18nProvider';

export function OverviewPanel() {
  const { t } = useI18n();
  return (
    <div className="space-y-4">
      <div className="rounded-xl border border-accent-200 bg-accent-50/60 p-5 dark:border-accent-800 dark:bg-accent-950/30">
        <h2 className="text-base font-semibold text-gray-900 dark:text-white">
          Win more applications with AI matches
        </h2>
        <p className="mt-2 text-sm text-gray-700 dark:text-gray-300">
          We continuously score new opportunities against your CV and preferences. Only roles above
          your quality threshold land in{' '}
          <a
            href="/dashboard/#matches"
            className="font-medium text-accent-700 underline dark:text-accent-400"
          >
            Matches
          </a>
          . Open them, apply with one click, and we email a weekly summary of the best fits.
        </p>
        <a
          href="/dashboard/#matches"
          className="mt-3 inline-flex min-h-[44px] items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white hover:bg-navy-800 dark:bg-accent-600 dark:hover:bg-accent-500"
        >
          View my matches →
        </a>
      </div>
      <div className="rounded-xl border-0 bg-white p-6 shadow-sm ring-1 ring-gray-200 dark:bg-navy-900 dark:ring-navy-700">
        <h2 className="text-lg font-semibold tracking-tight text-gray-900 dark:text-white">
          {t('dash.gettingStarted')}
        </h2>
        <ul className="mt-4 space-y-2 sm:space-y-3">
          <li className="flex items-start gap-3 text-sm text-gray-600 dark:text-gray-300">
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-accent-400 to-accent-600 text-xs font-bold text-white">
              1
            </span>
            <span>
              {t('dash.gsStep1a')}{' '}
              <a
                href="/dashboard/#settings"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
              >
                {t('dash.gsProfile')}
              </a>{' '}
              {t('dash.gsStep1b')}
            </span>
          </li>
          <li className="flex items-start gap-3 text-sm text-gray-600 dark:text-gray-300">
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-accent-400 to-accent-600 text-xs font-bold text-white">
              2
            </span>
            <span>
              {t('dash.gsStep2a')}{' '}
              <a
                href="/dashboard/#preferences"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
              >
                {t('dash.gsPreferences')}
              </a>{' '}
              {t('dash.gsStep2b')}
            </span>
          </li>
          <li className="flex items-start gap-3 text-sm text-gray-600 dark:text-gray-300">
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-accent-400 to-accent-600 text-xs font-bold text-white">
              3
            </span>
            <span>
              Open{' '}
              <a
                href="/dashboard/#matches"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
              >
                Matches
              </a>{' '}
              and hit <strong>Find matches now</strong> — then apply to the best fits.
            </span>
          </li>
        </ul>
      </div>
    </div>
  );
}
