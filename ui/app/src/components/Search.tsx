import { useEffect, useRef, useState, type ReactNode } from 'react';
import type { Facets } from '@/types/search';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import { useFocusTrap } from '@/hooks/useFocusTrap';
import { useSearchURLParams } from '@/hooks/useSearchURLParams';
import Cascade from './Cascade';
import { FiltersPanel } from './search/FiltersPanel';
import { SearchForm } from './search/SearchForm';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * /search/ -- query + filters + facets + pagination. Reads initial state
 * from the URL so deep links are shareable; writes back on changes via
 * history.replaceState so the back button stays predictable without full
 * navigations.
 */
export default function Search() {
  const { t } = useI18n();
  const [params, setParams] = useSearchURLParams();
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [facets, setFacets] = useState<Facets | undefined>();

  const { preferredCountries, preferredLanguages } = useCandidateProfile();

  const hasActiveFilters = Boolean(
    params.category ||
    params.remote_type ||
    params.employment_type ||
    params.seniority ||
    params.country ||
    params.q
  );

  function clearAll() {
    setParams({ sort: params.sort });
  }

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <header className="mb-6">
        <h1 className="sr-only">{t('search.searchJobs')}</h1>
        <SearchForm value={params} onChange={(next) => setParams(next)} />
      </header>

      <div className="flex items-center justify-between border-b border-gray-200 pb-3 md:hidden">
        <button
          type="button"
          onClick={() => setFiltersOpen(true)}
          className="inline-flex items-center gap-2 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 shadow-sm"
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
              d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.172a2 2 0 01-.586 1.414l-5.828 5.828A2 2 0 0013 14.828V17l-4 4v-6.172a2 2 0 00-.586-1.414L2.586 7.586A2 2 0 012 6.172V4z"
            />
          </svg>
          {t('search.filters')}
          {hasActiveFilters && (
            <span className="rounded-full bg-navy-900 px-2 py-0.5 text-xs text-white">&bull;</span>
          )}
        </button>
        {hasActiveFilters && (
          <button
            type="button"
            onClick={clearAll}
            className="text-sm text-gray-600 hover:text-gray-900"
          >
            {t('search.clearAll')}
          </button>
        )}
      </div>

      <div className="grid gap-8 md:grid-cols-[260px_1fr]">
        <aside className="hidden md:block">
          <FiltersPanel
            params={params}
            setParams={setParams}
            facets={facets}
            hasActiveFilters={hasActiveFilters}
            onClear={clearAll}
          />
        </aside>

        <section>
          <Cascade
            filters={{
              q: params.q,
              category: params.category,
              remote_type: params.remote_type,
              employment_type: params.employment_type,
              seniority: params.seniority,
              sort: params.sort,
            }}
            preferredCountries={preferredCountries}
            preferredLanguages={preferredLanguages}
            tierLimit={25}
            onFacets={setFacets}
          />
        </section>
      </div>

      {filtersOpen && (
        <MobileDrawer onClose={() => setFiltersOpen(false)} t={t}>
          <FiltersPanel
            params={params}
            setParams={(next) => {
              setParams(next);
            }}
            facets={facets}
            hasActiveFilters={hasActiveFilters}
            onClear={clearAll}
          />
          <div className="sticky bottom-0 mt-6 flex gap-3 border-t border-gray-200 bg-white px-4 py-3">
            <button
              type="button"
              onClick={clearAll}
              className="flex-1 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700"
            >
              {t('search.clearAll')}
            </button>
            <button
              type="button"
              onClick={() => setFiltersOpen(false)}
              className="flex-1 rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white"
            >
              {t('search.showResults')}
            </button>
          </div>
        </MobileDrawer>
      )}
    </div>
  );
}

function MobileDrawer({
  children,
  onClose,
  t,
}: {
  children: ReactNode;
  onClose: () => void;
  t: (k: import('@/i18n/strings').StringKey) => string;
}) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  useFocusTrap(panelRef, true, onClose);
  useEffect(() => {
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = '';
    };
  }, []);
  return (
    <div
      className="fixed inset-0 z-50 flex flex-col bg-black/40 md:hidden animate-fade-in"
      role="dialog"
      aria-modal="true"
      aria-label={t('search.filters')}
      onClick={onClose}
    >
      <div
        ref={panelRef}
        className="mt-auto flex max-h-[92vh] flex-col overflow-hidden rounded-t-2xl bg-white animate-slide-up"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-gray-200 px-4 py-3">
          <h2 className="text-base font-semibold text-gray-900">{t('search.filters')}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label={t('cta.close')}
            className="rounded p-1 text-gray-500 hover:bg-gray-100"
          >
            <svg
              className="h-5 w-5"
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
        </div>
        <div className="flex-1 overflow-y-auto px-4 py-4">{children}</div>
      </div>
    </div>
  );
}
