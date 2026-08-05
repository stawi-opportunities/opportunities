import { useQuery } from '@tanstack/react-query';
import { listCategories } from '@/api/search';
import { useI18n } from '@/i18n/I18nProvider';

export default function CategoryIndex() {
  const { t } = useI18n();
  const q = useQuery({
    queryKey: ['categories'],
    queryFn: () => listCategories(),
    staleTime: 5 * 60_000,
    retry: 1,
  });
  const cats = q.data?.categories ?? [];

  return (
    <div className="mx-auto max-w-5xl px-4 py-10 sm:px-6 lg:px-8">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-bold text-main">{t('category.browseByCategory')}</h1>
        <a href="/search/" className="btn-primary">
          Search
        </a>
      </div>

      {q.isLoading && (
        <div className="mt-8 grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="h-16 animate-pulse rounded-lg bg-surface-muted" />
          ))}
        </div>
      )}

      {!q.isLoading && cats.length > 0 && (
        <div className="mt-8 grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
          {cats.map((c) => (
            <a
              key={c.key}
              href={`/categories/${encodeURIComponent(c.key)}/`}
              className="rounded-lg border border-muted bg-surface p-4 transition-colors hover:border-accent-500/50"
            >
              <div className="font-medium capitalize text-main">
                {c.key || t('category.uncategorised')}
              </div>
              <div className="mt-1 text-xs text-secondary">
                {c.count.toLocaleString()} {c.count === 1 ? t('category.job') : t('category.jobs')}
              </div>
            </a>
          ))}
        </div>
      )}

      {!q.isLoading && cats.length === 0 && (
        <p className="mt-10 text-center text-sm text-secondary">
          No categories yet.{' '}
          <a href="/search/" className="font-medium text-accent-500 underline">
            Search jobs
          </a>
        </p>
      )}
    </div>
  );
}
