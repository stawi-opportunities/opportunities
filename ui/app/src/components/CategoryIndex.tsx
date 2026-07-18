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
    <div className="bg-white">
      <div className="border-b border-gray-100 bg-slate-50 px-4 py-12 sm:px-6 lg:px-8">
        <div className="mx-auto max-w-5xl text-center">
          <h1 className="text-4xl font-bold tracking-tight text-gray-900">
            {t('category.browseByCategory')}
          </h1>
          <p className="mt-3 text-lg text-gray-500">{t('category.discover')}</p>
          <div className="mt-6 flex flex-wrap justify-center gap-3">
            <a
              href="/search/"
              className="inline-flex items-center gap-2 rounded-lg bg-navy-900 px-5 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-navy-800"
            >
              <svg
                className="h-4 w-4"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <circle cx="11" cy="11" r="8" />
                <path strokeLinecap="round" strokeLinejoin="round" d="m21 21-4.35-4.35" />
              </svg>
              {t('category.findJobs')}
            </a>
            <a
              href="/jobs/"
              className="inline-flex items-center gap-2 rounded-lg border border-gray-300 bg-white px-5 py-2.5 text-sm font-semibold text-gray-800 transition-colors hover:bg-gray-50"
            >
              {t('footer.browseJobs')}
            </a>
          </div>
        </div>
      </div>

      <div className="mx-auto max-w-5xl px-4 py-12 sm:px-6 lg:px-8">
        {q.isLoading && (
          <div>
            <div className="h-5 w-40 animate-pulse rounded bg-gray-100" />
            <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
              {Array.from({ length: 8 }).map((_, i) => (
                <div key={i} className="h-16 animate-pulse rounded-lg bg-gray-100" />
              ))}
            </div>
          </div>
        )}

        {!q.isLoading && cats.length > 0 && (
          <div>
            <h2 className="text-xl font-semibold text-gray-900">
              {t('category.browseByIndustry')}
            </h2>
            <p className="mt-1 text-sm text-gray-500">{t('category.refineBySector')}</p>
            <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
              {cats.map((c) => (
                <a
                  key={c.key}
                  href={`/categories/${encodeURIComponent(c.key)}/`}
                  className="group flex flex-col justify-between rounded-lg border border-gray-200 bg-white p-4 transition-all hover:border-navy-300 hover:shadow-sm"
                >
                  <div className="font-medium capitalize text-gray-900 group-hover:text-navy-900">
                    {c.key || t('category.uncategorised')}
                  </div>
                  <div className="mt-1 text-xs text-gray-400">
                    {c.count.toLocaleString()}{' '}
                    {c.count === 1 ? t('category.job') : t('category.jobs')}
                  </div>
                </a>
              ))}
            </div>
          </div>
        )}

        {!q.isLoading && cats.length === 0 && (
          <div className="rounded-xl border border-dashed border-gray-200 bg-slate-50 px-6 py-12 text-center">
            <p className="text-base font-medium text-gray-800">{t('category.emptyTitle')}</p>
            <p className="mt-2 text-sm text-gray-500">{t('category.emptyBody')}</p>
            <a
              href="/search/"
              className="mt-6 inline-flex rounded-lg bg-navy-900 px-5 py-2.5 text-sm font-semibold text-white hover:bg-navy-800"
            >
              {t('category.findJobs')}
            </a>
          </div>
        )}
      </div>
    </div>
  );
}
