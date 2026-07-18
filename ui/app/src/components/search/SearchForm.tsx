import { useEffect, useState } from 'react';
import type { SearchParams } from '@/types/search';
import { useI18n } from '@/i18n/I18nProvider';

export function SearchForm({
  value,
  onChange,
}: {
  value: SearchParams;
  onChange: (next: SearchParams) => void;
}) {
  const { t } = useI18n();
  const [q, setQ] = useState(value.q ?? '');
  const [l, setL] = useState(value.l ?? '');
  useEffect(() => setQ(value.q ?? ''), [value.q]);
  useEffect(() => setL(value.l ?? ''), [value.l]);

  function submit() {
    onChange({
      ...value,
      q: q.trim() || undefined,
      l: l.trim() || undefined,
      offset: 0,
    });
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
      className="w-full"
      role="search"
    >
      <div className="flex flex-col overflow-hidden rounded-xl border border-gray-300 bg-white shadow-sm focus-within:border-navy-900 focus-within:ring-1 focus-within:ring-navy-900 sm:flex-row sm:items-stretch">
        {/* What */}
        <div className="relative flex min-w-0 flex-1 items-center border-b border-gray-200 sm:border-b-0 sm:border-r">
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
            placeholder={t('search.whatPlaceholder')}
            className="w-full bg-transparent py-3 pl-10 pr-3 text-base text-gray-900 placeholder-gray-400 focus:outline-none"
            aria-label={t('search.whatPlaceholder')}
          />
        </div>
        {/* Where */}
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
              d="M17.657 16.657L13.414 20.9a1.998 1.998 0 01-2.827 0l-4.244-4.243a8 8 0 1111.314 0z"
            />
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="2"
              d="M15 11a3 3 0 11-6 0 3 3 0 016 0z"
            />
          </svg>
          <input
            type="search"
            value={l}
            onChange={(e) => setL(e.target.value)}
            placeholder={t('search.wherePlaceholder')}
            className="w-full bg-transparent py-3 pl-10 pr-3 text-base text-gray-900 placeholder-gray-400 focus:outline-none"
            aria-label={t('search.wherePlaceholder')}
          />
        </div>
        <div className="flex p-1.5 sm:pl-0">
          <button
            type="submit"
            className="min-h-[44px] w-full rounded-lg bg-navy-900 px-5 py-2.5 text-sm font-semibold text-white shadow-sm hover:bg-navy-800 sm:w-auto"
          >
            {t('search.searchButton')}
          </button>
        </div>
      </div>
    </form>
  );
}
