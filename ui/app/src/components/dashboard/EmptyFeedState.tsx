import type { StringKey } from '@/i18n/strings';
import type { OpportunityFilter } from '@/api/candidates';

interface Props {
  filter: OpportunityFilter;
  t: (k: StringKey, fallback?: string) => string;
}

export function EmptyFeedState({ filter, t }: Props) {
  const showTryAll = filter !== 'all';

  return (
    <div className="rounded-lg border border-gray-200 bg-white p-6 text-center dark:border-navy-700 dark:bg-navy-900">
      <svg
        className="mx-auto h-24 w-24 text-gray-300 dark:text-navy-600"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 120 120"
        aria-hidden="true"
      >
        <circle cx="60" cy="40" r="16" strokeWidth="2" />
        <path d="M30 90c0-16.6 13.4-30 30-30s30 13.4 30 30" strokeWidth="2" strokeLinecap="round" />
        <rect x="44" y="72" width="32" height="20" rx="3" strokeWidth="1.5" strokeDasharray="3 2" />
      </svg>

      <h3 className="mt-4 text-base font-semibold text-gray-900 dark:text-white">
        {t('dash.emptyFeedTitle')}
      </h3>
      <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">{t('dash.emptyFeedHint')}</p>

      {showTryAll && (
        <p className="mt-2 text-xs text-gray-500 dark:text-gray-400">
          <a
            href="/dashboard/#feed"
            onClick={(e) => {
              e.preventDefault();
              const url = new URL(window.location.href);
              url.hash = 'feed';
              url.searchParams.delete('filter');
              window.history.pushState({}, '', url.toString());
              window.dispatchEvent(new HashChangeEvent('hashchange'));
            }}
            className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
          >
            {t('feed.tryAllFilter')}
          </a>
          {' · '}
          <a
            href="/dashboard/#matches"
            className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
          >
            Find matches now
          </a>
        </p>
      )}

      <div className="mt-6 grid gap-3 sm:grid-cols-3">
        <a
          href="/onboarding/"
          className="rounded-lg border border-gray-200 p-4 text-left transition-colors hover:border-gray-300 hover:bg-gray-50 dark:border-navy-700 dark:hover:border-navy-600 dark:hover:bg-navy-800"
        >
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300">
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"
              />
            </svg>
          </div>
          <p className="mt-2 text-sm font-medium text-gray-900 dark:text-white">
            {t('dash.emptyFeedCompleteProfile')}
          </p>
          <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {t('onboard.aboutYouHint')}
          </p>
        </a>

        <a
          href="#preferences"
          onClick={(e) => {
            e.preventDefault();
            const panel = document.getElementById('match-preferences');
            if (panel) panel.scrollIntoView({ behavior: 'smooth' });
          }}
          className="rounded-lg border border-gray-200 p-4 text-left transition-colors hover:border-gray-300 hover:bg-gray-50 dark:border-navy-700 dark:hover:border-navy-600 dark:hover:bg-navy-800"
        >
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"
              />
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
              />
            </svg>
          </div>
          <p className="mt-2 text-sm font-medium text-gray-900 dark:text-white">
            {t('dash.emptyFeedSetPreferences')}
          </p>
          <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {t('dash.matchPreferencesHint')}
          </p>
        </a>

        <a
          href="/jobs/"
          className="rounded-lg border border-gray-200 p-4 text-left transition-colors hover:border-gray-300 hover:bg-gray-50 dark:border-navy-700 dark:hover:border-navy-600 dark:hover:bg-navy-800"
        >
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300">
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
              />
            </svg>
          </div>
          <p className="mt-2 text-sm font-medium text-gray-900 dark:text-white">
            {t('dash.emptyFeedBrowseAll')}
          </p>
          <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{t('cta.browseAll')}</p>
        </a>
      </div>
    </div>
  );
}
