import { useQuery } from '@tanstack/react-query';
import { listCategories } from '@/api/search';
import { useI18n } from '@/i18n/I18nProvider';

export default function CategoryIndex() {
  const { t } = useI18n();
  const q = useQuery({
    queryKey: ['categories'],
    queryFn: () => listCategories(),
    staleTime: 5 * 60_000,
  });
  const cats = q.data?.categories ?? [];

  return (
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
      <h1 className="text-3xl font-bold">{t('category.browseByCategory')}</h1>
      {q.isLoading ? (
        <div className="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-20 animate-pulse rounded-lg bg-gray-100" />
          ))}
        </div>
      ) : cats.length === 0 ? (
        <p className="mt-8 text-gray-500">{t('category.noCategories')}</p>
      ) : (
        <div className="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3">
          {cats.map((c) => (
            <a
              key={c.key}
              href={`/categories/${encodeURIComponent(c.key)}/`}
              className="rounded-lg border border-gray-200 p-4 hover:border-navy-300"
            >
              <div className="font-semibold capitalize text-gray-900">
                {c.key || t('category.uncategorised')}
              </div>
              <div className="text-sm text-gray-500">
                {c.count.toLocaleString()} {t('common.jobs')}
              </div>
            </a>
          ))}
        </div>
      )}
    </div>
  );
}
