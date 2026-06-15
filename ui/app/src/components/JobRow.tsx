import type { SearchResult } from '@/types/search';
import { categoryLabel, fmtMoney, timeAgo } from '@/utils/format';
import { useI18n } from '@/i18n/I18nProvider';

/** Shared row renderer used by search results, category lists, home feed. */
export function JobRow({ result }: { result: SearchResult }) {
  const { t } = useI18n();
  const money = fmtMoney(result.salary_min, result.salary_max, result.currency);
  const hasSlug = !!result.slug;
  const href = hasSlug ? `/jobs/${encodeURIComponent(result.slug)}/` : undefined;

  const remoteLabel = (key: string) => {
    switch (key) {
      case 'remote':
        return t('search.remote');
      case 'hybrid':
        return t('search.hybrid');
      case 'onsite':
      case 'on_site':
        return t('search.onSite');
      default:
        return key;
    }
  };

  const Inner = (
    <div className="flex items-start gap-4">
      <CompanyAvatar company={result.company} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2">
          <span className="truncate text-base font-semibold text-navy-900">{result.title}</span>
          {result.is_featured && (
            <span className="inline-flex items-center gap-1 rounded-full bg-amber-50 px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-amber-700 ring-1 ring-amber-200">
              <svg className="h-3 w-3" fill="currentColor" viewBox="0 0 20 20"><path d="M9.049 2.927c.3-.921 1.603-.921 1.902 0l1.07 3.292a1 1 0 00.95.69h3.462c.969 0 1.371 1.24.588 1.81l-2.8 2.034a1 1 0 00-.364 1.118l1.07 3.292c.3.921-.755 1.688-1.54 1.118l-2.8-2.034a1 1 0 00-1.175 0l-2.8 2.034c-.784.57-1.838-.197-1.539-1.118l1.07-3.292a1 1 0 00-.364-1.118L2.98 8.72c-.783-.57-.38-1.81.588-1.81h3.461a1 1 0 00.951-.69l1.07-3.292z" /></svg>
              {t('common.featured')}
            </span>
          )}
        </div>
        <div className="mt-0.5 truncate text-sm text-gray-700">
          {result.company}
          {result.location_text && <span className="text-gray-500"> · {result.location_text}</span>}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-600">
          {result.category && <span>{categoryLabel(result.category)}</span>}
          {result.remote_type && <span>· {remoteLabel(result.remote_type)}</span>}
          {money && <span className="text-gray-800">· {money}</span>}
          {(result.views_24h ?? 0) > 0 && (
            <span className="text-gray-400">
              · {result.views_24h} view{result.views_24h !== 1 ? 's' : ''}
            </span>
          )}
          {(result.applies_24h ?? 0) > 0 && (
            <span className="text-emerald-700">· {result.applies_24h} applied</span>
          )}
        </div>
      </div>
      <time className="shrink-0 whitespace-nowrap text-xs text-gray-500">
        {timeAgo(result.posted_at)}
      </time>
    </div>
  );

  return (
    <li className="border-b border-gray-200 last:border-b-0">
      {hasSlug ? (
        <a
          href={href}
          className="block px-4 py-4 transition-colors hover:bg-gray-50 focus-visible:bg-gray-50 focus-visible:outline-none sm:px-6"
        >
          {Inner}
        </a>
      ) : (
        <div className="block px-4 py-4 opacity-75 sm:px-6" aria-label="Job listing (no link)">
          {Inner}
        </div>
      )}
    </li>
  );
}

function CompanyAvatar({ company }: { company: string }) {
  const initial = (company || '?').trim().slice(0, 1).toUpperCase();
  return (
    <div
      className="flex h-10 w-10 shrink-0 items-center justify-center rounded bg-navy-100 text-sm font-semibold text-navy-900"
      aria-hidden="true"
    >
      {initial}
    </div>
  );
}
