import { useQuery } from '@tanstack/react-query';
import { listCategories } from '@/api/search';
import { useI18n } from '@/i18n/I18nProvider';

const OPPORTUNITY_TYPES = [
  {
    href: "/jobs/",
    emoji: "💼",
    label: "Jobs",
    description: "Full-time, part-time, remote & contract roles across every industry.",
    color: "bg-blue-50 border-blue-100 hover:border-blue-300 hover:bg-blue-50/80",
    badge: "bg-blue-100 text-blue-700",
  },
  {
    href: "/scholarships/",
    emoji: "🎓",
    label: "Scholarships",
    description: "Grants, bursaries and fellowships for students and researchers.",
    color: "bg-green-50 border-green-100 hover:border-green-300 hover:bg-green-50/80",
    badge: "bg-green-100 text-green-700",
  },
  {
    href: "/tenders/",
    emoji: "📋",
    label: "Tenders",
    description: "Government and private sector RFPs, bids and procurement notices.",
    color: "bg-orange-50 border-orange-100 hover:border-orange-300 hover:bg-orange-50/80",
    badge: "bg-orange-100 text-orange-700",
  },
  {
    href: "/deals/",
    emoji: "🏷️",
    label: "Deals",
    description: "Curated discounts, offers and partnerships for professionals.",
    color: "bg-pink-50 border-pink-100 hover:border-pink-300 hover:bg-pink-50/80",
    badge: "bg-pink-100 text-pink-700",
  },
  {
    href: "/funding/",
    emoji: "💰",
    label: "Funding",
    description: "Grants, venture capital and investor opportunities for ventures.",
    color: "bg-purple-50 border-purple-100 hover:border-purple-300 hover:bg-purple-50/80",
    badge: "bg-purple-100 text-purple-700",
  },
] as const;

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
<<<<<<< HEAD
    <div className="bg-white">
      {/* Page header */}
      <div className="border-b border-gray-100 bg-slate-50 px-4 py-12 sm:px-6 lg:px-8">
        <div className="mx-auto max-w-5xl text-center">
          <h1 className="text-4xl font-bold tracking-tight text-gray-900">Browse by category</h1>
          <p className="mt-3 text-lg text-gray-500">
            Discover opportunities across jobs, scholarships, tenders, deals and funding — all in one place.
          </p>
          <div className="mt-6 flex justify-center">
=======
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
>>>>>>> upstream/main
            <a
              href="/search/"
              className="inline-flex items-center gap-2 rounded-lg bg-navy-900 px-5 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-navy-800"
            >
<<<<<<< HEAD
              <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
                <circle cx="11" cy="11" r="8" /><path strokeLinecap="round" strokeLinejoin="round" d="m21 21-4.35-4.35" />
              </svg>
              Advanced search
=======
              <div className="font-semibold capitalize text-gray-900">
                {c.key || t('category.uncategorised')}
              </div>
              <div className="text-sm text-gray-500">
                {c.count.toLocaleString()} {t('common.jobs')}
              </div>
>>>>>>> upstream/main
            </a>
          </div>
        </div>
      </div>

      <div className="mx-auto max-w-5xl px-4 py-12 sm:px-6 lg:px-8">

        {/* Opportunity types — always shown */}
        <div>
          <h2 className="text-xl font-semibold text-gray-900">Opportunity types</h2>
          <p className="mt-1 text-sm text-gray-500">Choose the kind of opportunity you are looking for.</p>
          <div className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {OPPORTUNITY_TYPES.map(({ href, emoji, label, description, color, badge }) => (
              <a
                key={href}
                href={href}
                className={`group flex flex-col rounded-xl border p-5 transition-all duration-150 ${color}`}
              >
                <div className="flex items-center gap-3">
                  <span className={`flex h-10 w-10 items-center justify-center rounded-lg text-xl ${badge}`}>
                    {emoji}
                  </span>
                  <span className="text-base font-semibold text-gray-900 group-hover:text-navy-900">
                    {label}
                  </span>
                  <svg
                    className="ml-auto h-4 w-4 text-gray-400 transition-transform group-hover:translate-x-0.5 group-hover:text-navy-600"
                    fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor"
                  >
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9 18l6-6-6-6" />
                  </svg>
                </div>
                <p className="mt-3 text-sm leading-relaxed text-gray-500">{description}</p>
              </a>
            ))}
          </div>
        </div>

        {/* Industry/keyword categories from API — only shown when data exists */}
        {q.isLoading && (
          <div className="mt-14">
            <div className="h-5 w-40 animate-pulse rounded bg-gray-100" />
            <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
              {Array.from({ length: 8 }).map((_, i) => (
                <div key={i} className="h-16 animate-pulse rounded-lg bg-gray-100" />
              ))}
            </div>
          </div>
        )}

        {!q.isLoading && cats.length > 0 && (
          <div className="mt-14">
            <h2 className="text-xl font-semibold text-gray-900">Browse by industry</h2>
            <p className="mt-1 text-sm text-gray-500">Refine your search by sector or field.</p>
            <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
              {cats.map((c) => (
                <a
                  key={c.key}
                  href={`/categories/${encodeURIComponent(c.key)}/`}
                  className="group flex flex-col justify-between rounded-lg border border-gray-200 bg-white p-4 transition-all hover:border-navy-300 hover:shadow-sm"
                >
                  <div className="font-medium capitalize text-gray-900 group-hover:text-navy-900">
                    {c.key || "Uncategorised"}
                  </div>
                  <div className="mt-1 text-xs text-gray-400">
                    {c.count.toLocaleString()} {c.count === 1 ? "opportunity" : "opportunities"}
                  </div>
                </a>
              ))}
            </div>
          </div>
        )}

      </div>
    </div>
  );
}
