import { useEffect, useState } from 'react';
import type { SearchParams } from '@/types/search';
import { searchQueryText } from '@/types/search';
import { useI18n } from '@/i18n/I18nProvider';

export function SearchForm({
  value,
  onChange,
}: {
  value: SearchParams;
  onChange: (next: SearchParams) => void;
}) {
  const { t } = useI18n();
  // Single box: fold legacy ?l= into the visible query so old links still work.
  const [q, setQ] = useState(() => searchQueryText(value) ?? '');
  useEffect(() => {
    setQ(searchQueryText(value) ?? '');
  }, [value.q, value.l]);

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onChange({
          ...value,
          q: q.trim() || undefined,
          l: undefined,
          offset: 0,
        });
      }}
      className="w-full"
      role="search"
    >
      <div className="flex overflow-hidden rounded-xl border border-gray-300 bg-white shadow-sm focus-within:border-navy-900 focus-within:ring-1 focus-within:ring-navy-900">
        <div className="relative flex min-w-0 flex-1 items-center">
          <svg
            className="pointer-events-none absolute left-3 h-5 w-5 text-gray-400"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="2"
              d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
            />
          </svg>
          <input
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t('search.searchPlaceholder')}
            className="w-full bg-transparent py-3 pl-10 pr-3 text-base text-gray-900 placeholder-gray-400 focus:outline-none"
            aria-label={t('search.searchJobs')}
          />
        </div>
        <div className="flex p-1.5">
          <button
            type="submit"
            className="min-h-[44px] rounded-lg bg-navy-900 px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-navy-800"
          >
            {t('search.searchButton')}
          </button>
        </div>
      </div>
    </form>
  );
}
