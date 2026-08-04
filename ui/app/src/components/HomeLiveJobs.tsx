import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { feed } from '@/api/search';
import { JobRow } from './JobRow';
import type { SearchResult } from '@/types/search';
import { categoryLabel } from '@/utils/format';
import { getVisitorLocale } from '@/utils/locale';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * Homepage live sample — anonymous browse-before-sign-in proof of life.
 * Fetches the public /api/feed (no auth required) and renders a small,
 * filterable list of real roles so a first-time visitor can answer "is
 * there something here for me" without handing over a CV. Filtering is
 * client-side over the sample (category + remote-type chips), and the
 * trailing CTA points at full /search/.
 */
export default function HomeLiveJobs() {
  const { t } = useI18n();
  const [category, setCategory] = useState<string | null>(null);
  const [remoteOnly, setRemoteOnly] = useState(false);

  const detected = useMemo(getVisitorLocale, []);

  const feedParams = useMemo(() => {
    const p: { tier_limit: number; country?: string; lang?: string } = { tier_limit: 25 };
    if (detected.country) p.country = detected.country;
    if (detected.languages.length) p.lang = detected.languages.join(',');
    return p;
  }, [detected.country, detected.languages]);

  const q = useQuery({
    queryKey: ['home-live-jobs', feedParams],
    queryFn: () => feed(feedParams),
    staleTime: 5 * 60_000,
    retry: 1,
  });

  // Flatten tiers into a single de-duped list of roles.
  const all: SearchResult[] = useMemo(() => {
    if (!q.data) return [];
    const seen = new Set<string>();
    const out: SearchResult[] = [];
    for (const tier of q.data.tiers) {
      for (const j of tier.jobs) {
        if (!seen.has(j.id)) {
          seen.add(j.id);
          out.push(j);
        }
      }
    }
    return out;
  }, [q.data]);

  // Keep the sample to ~15 roles so the section stays compact.
  const sample = all.slice(0, 15);

  const categories = useMemo(() => {
    const counts = new Map<string, number>();
    for (const j of sample) {
      if (j.category) counts.set(j.category, (counts.get(j.category) ?? 0) + 1);
    }
    return Array.from(counts.entries()).sort((a, b) => b[1] - a[1]);
  }, [sample]);

  const filtered = useMemo(() => {
    return sample.filter((j) => {
      if (category && j.category !== category) return false;
      if (remoteOnly && j.remote_type !== 'remote') return false;
      return true;
    });
  }, [sample, category, remoteOnly]);

  const isLoading = q.isLoading && !q.data;

  return (
    <section
      id="live-jobs"
      className="mx-auto max-w-4xl scroll-mt-24 px-4 pb-20 pt-4 sm:px-6 lg:px-8"
      aria-labelledby="live-jobs-title"
    >
      <header className="flex flex-col items-start justify-between gap-4 sm:flex-row sm:items-end">
        <div>
          <h2
            id="live-jobs-title"
            className="font-display text-3xl font-semibold tracking-tight text-main sm:text-4xl"
          >
            {t('home.sampleTitle')}
          </h2>
          <p className="mt-2 max-w-xl text-sm text-secondary sm:text-base">
            {t('home.sampleSubtitle')}
          </p>
        </div>
        <a href="/search/" className="btn-secondary shrink-0">
          {t('home.browseAll')}
          <span aria-hidden="true"> →</span>
        </a>
      </header>

      {!isLoading && categories.length > 0 && (
        <div className="mt-6 flex flex-wrap items-center gap-2" role="group" aria-label="Filters">
          <button
            type="button"
            onClick={() => setCategory(null)}
            aria-pressed={category === null}
            className={[
              'inline-flex items-center rounded-full border px-4 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500',
              category === null
                ? 'border-accent-500 bg-accent-500/10 text-accent-500'
                : 'border-muted-strong bg-surface text-secondary hover:border-accent-500/50 hover:text-main',
            ].join(' ')}
          >
            {t('home.filterAll')}
          </button>
          {categories.map(([key]) => {
            const on = category === key;
            return (
              <button
                key={key}
                type="button"
                onClick={() => setCategory(on ? null : key)}
                aria-pressed={on}
                className={[
                  'inline-flex items-center rounded-full border px-4 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500',
                  on
                    ? 'border-accent-500 bg-accent-500/10 text-accent-500'
                    : 'border-muted-strong bg-surface text-secondary hover:border-accent-500/50 hover:text-main',
                ].join(' ')}
              >
                {categoryLabel(key)}
              </button>
            );
          })}
          <button
            type="button"
            onClick={() => setRemoteOnly((v) => !v)}
            aria-pressed={remoteOnly}
            className={[
              'inline-flex items-center gap-1.5 rounded-full border px-4 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500',
              remoteOnly
                ? 'border-accent-500 bg-accent-500/10 text-accent-500'
                : 'border-muted-strong bg-surface text-secondary hover:border-accent-500/50 hover:text-main',
            ].join(' ')}
          >
            <svg
              className="h-3.5 w-3.5"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              aria-hidden="true"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M3 15a9 9 0 1018 0h-3M3 15h3"
              />
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M12 6V3m0 0l-2.5 2.5M12 3l2.5 2.5M5.5 8.5L3.5 7m0 0l-1 1M18.5 8.5l2-1.5m0 0l1 1"
              />
            </svg>
            {t('home.filterRemote')}
          </button>
        </div>
      )}

      <div className="mt-6">
        {isLoading && (
          <div className="space-y-px overflow-hidden rounded-lg border border-muted">
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="h-20 animate-pulse bg-surface" />
            ))}
          </div>
        )}

        {!isLoading && q.isError && (
          <div className="rounded-lg border border-muted bg-surface p-8 text-center">
            <p className="text-sm text-secondary">
              {q.error instanceof Error ? q.error.message : 'Could not load jobs.'}
            </p>
            <button
              type="button"
              onClick={() => void q.refetch()}
              className="btn-secondary mt-4 min-h-[44px]"
            >
              {t('cta.retry')}
            </button>
          </div>
        )}

        {!isLoading && !q.isError && filtered.length === 0 && (
          <div className="rounded-lg border border-muted bg-surface p-8 text-center">
            <p className="text-sm text-secondary">{t('search.noResults')}</p>
          </div>
        )}

        {!isLoading && filtered.length > 0 && (
          <ul className="overflow-hidden rounded-lg border border-muted bg-surface">
            {filtered.map((j) => (
              <JobRow key={j.id} result={j} />
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
