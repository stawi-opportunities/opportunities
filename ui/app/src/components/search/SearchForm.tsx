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
      <div className="flex overflow-hidden rounded-xl border border-muted bg-surface shadow-sm focus-within:border-accent-500 focus-within:ring-1 focus-within:ring-accent-500">
        <div className="relative flex min-w-0 flex-1 items-center">
          <svg
            className="pointer-events-none absolute left-3 h-5 w-5 text-secondary"
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
            className="w-full bg-transparent py-3 pl-10 pr-3 text-base text-main placeholder-secondary focus:outline-none"
            aria-label={t('search.searchJobs')}
          />
        </div>
        <div className="flex p-1.5">
          <button type="submit" className="btn-primary min-h-[44px] px-5">
            {t('search.searchButton')}
          </button>
        </div>
      </div>
    </form>
  );
}
