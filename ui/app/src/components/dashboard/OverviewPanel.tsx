import { useI18n } from '@/i18n/I18nProvider';

export function OverviewPanel() {
  const { t } = useI18n();
  return (
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
            <a
              href="/dashboard/#feed"
              className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
            >
              {t('dash.gsBrowse')}
            </a>{' '}
            {t('dash.gsStep3b')}
          </span>
        </li>
      </ul>
    </div>
  );
}
