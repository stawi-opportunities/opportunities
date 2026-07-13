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
  useEffect(() => setQ(value.q ?? ''), [value.q]);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onChange({ ...value, q: q.trim() || undefined, offset: 0 });
      }}
      className="flex gap-2"
      role="search"
    >
      <div className="relative flex-1">
        <svg
          className="pointer-events-none absolute left-3 top-1/2 h-5 w-5 -translate-y-1/2 text-gray-400"
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
          className="w-full rounded-md border border-gray-300 bg-white py-2.5 pl-10 pr-10 text-base shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
          aria-label={t('search.searchJobs')}
        />
        {q && (
          <button
            type="button"
            onClick={() => {
              setQ('');
              onChange({ ...value, q: undefined, offset: 0 });
            }}
            aria-label={t('search.clear')}
            className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-gray-400 hover:text-gray-600"
          >
            <svg
              className="h-4 w-4"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              aria-hidden="true"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M6 18L18 6M6 6l12 12"
              />
            </svg>
          </button>
        )}
      </div>
      <button
        type="submit"
        className="rounded-md bg-navy-900 px-5 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
      >
        {t('search.searchButton')}
      </button>
    </form>
  );
}
