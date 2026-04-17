import type { SearchResult } from "@/types/search";
import { categoryLabel, fmtMoney, timeAgo } from "@/utils/format";

const REMOTE_LABELS: Record<string, string> = {
  remote: "Remote",
  hybrid: "Hybrid",
  onsite: "On-site",
  on_site: "On-site",
};

/** Shared row renderer used by search results, category lists, home feed. */
export function JobRow({ result }: { result: SearchResult }) {
  const money = fmtMoney(result.salary_min, result.salary_max, result.currency);
  const hasSlug = !!result.slug;
  const href = hasSlug ? `/jobs/${encodeURIComponent(result.slug)}/` : undefined;

  const Inner = (
    <div className="flex items-start gap-4">
      <CompanyAvatar company={result.company} />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2">
          <span className="truncate text-base font-semibold text-navy-900">
            {result.title}
          </span>
          {result.is_featured && (
            <span className="text-xs font-medium uppercase tracking-wide text-accent-700">
              Featured
            </span>
          )}
        </div>
        <div className="mt-0.5 truncate text-sm text-gray-700">
          {result.company}
          {result.location_text && (
            <span className="text-gray-500"> · {result.location_text}</span>
          )}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-600">
          {result.category && (
            <span>{categoryLabel(result.category)}</span>
          )}
          {result.remote_type && (
            <span>· {REMOTE_LABELS[result.remote_type] ?? result.remote_type}</span>
          )}
          {money && <span className="text-gray-800">· {money}</span>}
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
  const initial = (company || "?").trim().slice(0, 1).toUpperCase();
  return (
    <div
      className="flex h-10 w-10 shrink-0 items-center justify-center rounded bg-navy-100 text-sm font-semibold text-navy-900"
      aria-hidden="true"
    >
      {initial}
    </div>
  );
}
