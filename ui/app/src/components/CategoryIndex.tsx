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
        <h1 className="text-2xl font-bold text-gray-900">{t('category.browseByCategory')}</h1>
        <a
          href="/search/"
          className="rounded-md bg-navy-900 px-4 py-2 text-sm font-semibold text-white hover:bg-navy-800"
        >
          Search
        </a>
      </div>

      {q.isLoading && (
        <div className="mt-8 grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="h-16 animate-pulse rounded-lg bg-gray-100" />
          ))}
        </div>
      )}

      {!q.isLoading && cats.length > 0 && (
        <div className="mt-8 grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
          {cats.map((c) => (
            <a
              key={c.key}
              href={`/categories/${encodeURIComponent(c.key)}/`}
              className="rounded-lg border border-gray-200 bg-white p-4 hover:border-navy-300"
            >
              <div className="font-medium capitalize text-gray-900">
                {c.key || t('category.uncategorised')}
              </div>
              <div className="mt-1 text-xs text-gray-400">
                {c.count.toLocaleString()} {c.count === 1 ? t('category.job') : t('category.jobs')}
              </div>
            </a>
          ))}
        </div>
      )}

      {!q.isLoading && cats.length === 0 && (
        <p className="mt-10 text-center text-sm text-gray-500">
          No categories yet.{' '}
          <a href="/search/" className="font-medium text-navy-900 underline">
            Search jobs
          </a>
        </p>
      )}
    </div>
  );
}
