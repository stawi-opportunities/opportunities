import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { categoryJobs } from '@/api/search';
import { JobRow } from './JobRow';
import { categoryLabel } from '@/utils/format';
import { useI18n } from '@/i18n/I18nProvider';
import type { SearchParams } from '@/types/search';
import { SortPicker } from '@/components/ui/SortPicker';

export default function CategoryPage() {
  const { t } = useI18n();
  const slug = (() => {
    const m = window.location.pathname.match(/^\/categories\/([^/]+)\/?$/);
    return m ? decodeURIComponent(m[1]!) : null;
  })();
  const [cursor, setCursor] = useState<string | undefined>();
  const [sort, setSort] = useState<SearchParams['sort']>('recent');

  const q = useQuery({
    queryKey: ['category-jobs', slug, cursor, sort],
    queryFn: () => categoryJobs(slug!, { cursor, limit: 25, sort }),
    enabled: !!slug,
    staleTime: 30_000,
  });

  if (!slug) return <NotFound t={t} />;
  const title = categoryLabel(slug);

  return (
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
      <nav aria-label="Breadcrumb" className="text-sm text-secondary">
        <a href="/" className="hover:text-main">
          {t('common.home')}
        </a>
        <span className="mx-1.5">/</span>
        <a href="/categories/" className="hover:text-main">
          {t('common.categories')}
        </a>
        <span className="mx-1.5">/</span>
        <span className="text-main">{title}</span>
      </nav>
      <h1 className="mt-3 text-3xl font-bold text-main">
        {title} {t('common.jobs')}
      </h1>
      <p className="mt-2 text-secondary">
        {t('category.latestRoles').replace('{category}', title.toLowerCase())}
      </p>

      <div className="mt-4">
        <SortPicker
          value={sort}
          onChange={(v) => {
            setSort(v);
            setCursor(undefined);
          }}
        />
      </div>

      {q.isLoading && <SkeletonList />}
      {q.isError && (
        <div className="mt-8 rounded-md bg-accent-500/10 p-4 text-sm text-red-500" role="alert">
          {t('error.categoryLoad')}{' '}
          <button
            type="button"
            onClick={() => q.refetch()}
            className="font-medium underline hover:text-red-400"
          >
            {t('cta.retry')}
          </button>
        </div>
      )}
      {q.data && (
        <>
          <ul className="mt-6 overflow-hidden rounded-lg border border-muted bg-surface">
            {q.data.results.map((r) => (
              <JobRow key={r.id} result={r} />
            ))}
            {q.data.results.length === 0 && (
              <li className="px-4 py-10 text-center">
                <p className="text-sm text-secondary">
                  {t('category.noRolesOpen').replace('{category}', title.toLowerCase())}
                </p>
                <a
                  href="/jobs/"
                  className="mt-3 inline-block text-sm font-medium text-accent-500 hover:text-accent-400"
                >
                  {t('category.browseAllJobs')}
                </a>
              </li>
            )}
          </ul>
          {q.data.has_more && (
            <div className="mt-6 text-center">
              <button
                type="button"
                disabled={q.isFetching}
                className="btn-secondary min-h-[44px] px-5 disabled:opacity-60"
                onClick={() => setCursor(q.data.cursor_next)}
              >
                {q.isFetching ? t('common.loading') : t('cta.loadMore')}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function SkeletonList() {
  return (
    <ul className="mt-6 space-y-2">
      {Array.from({ length: 6 }).map((_, i) => (
        <li key={i} className="h-20 animate-pulse rounded-lg bg-surface-muted" />
      ))}
    </ul>
  );
}

function NotFound({ t }: { t: (k: import('@/i18n/strings').StringKey) => string }) {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-main">{t('category.notFound')}</h1>
      <p className="mt-2 text-secondary">{t('category.notFoundMessage')}</p>
      <a href="/categories/" className="btn-primary mt-6">
        {t('category.backToAll')}
      </a>
    </div>
  );
}
