import { useI18n } from '@/i18n/I18nProvider';

export function OverviewPanel() {
  const { t } = useI18n();
  return (
    <div className="space-y-4">
      <div className="rounded-xl border border-accent-200 bg-accent-50/60 p-5 dark:border-accent-800 dark:bg-accent-950/30">
        <h2 className="text-base font-semibold text-gray-900 dark:text-white">
          Real matches first — pay only if they help
        </h2>
        <p className="mt-2 text-sm text-gray-700 dark:text-gray-300">
          Upload a CV, run <strong>Find matches now</strong>, and review scored roles. Browse and
          free tools (CV score, job fit) always work. Subscribe when you want more weekly matches
          and digests.
        </p>
        <div className="mt-3 flex flex-wrap gap-2">
          <a
            href="/dashboard/#matches"
            className="inline-flex min-h-[44px] items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white hover:bg-navy-800 dark:bg-accent-600 dark:hover:bg-accent-500"
          >
            View my matches →
          </a>
          <a
            href="/dashboard/#tools"
            className="inline-flex min-h-[44px] items-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-800 hover:bg-gray-50 dark:border-navy-600 dark:bg-navy-900 dark:text-gray-200"
          >
            Free tools
          </a>
        </div>
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
              Upload a CV under{' '}
              <a
                href="/dashboard/#preferences"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
              >
                Preferences
              </a>
            </span>
          </li>
          <li className="flex items-start gap-3 text-sm text-gray-600 dark:text-gray-300">
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-accent-400 to-accent-600 text-xs font-bold text-white">
              2
            </span>
            <span>
              Open{' '}
              <a
                href="/dashboard/#matches"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
              >
                Matches
              </a>{' '}
              and hit <strong>Find matches now</strong> — free proof shortlist.
            </span>
          </li>
          <li className="flex items-start gap-3 text-sm text-gray-600 dark:text-gray-300">
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-accent-400 to-accent-600 text-xs font-bold text-white">
              3
            </span>
            <span>
              Use{' '}
              <a
                href="/dashboard/#tools"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
              >
                Tools
              </a>{' '}
              for CV ATS score and job-fit checks — then apply via employer links.
            </span>
          </li>
        </ul>
      </div>
    </div>
  );
}
