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
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="truncate text-base font-semibold text-gray-900 group-hover:text-accent-600">
            {result.title}
          </span>
          {result.is_featured && (
            <span className="rounded-full bg-yellow-50 px-2 py-0.5 text-xs font-medium text-yellow-800">
              ★ Featured
            </span>
          )}
        </div>
        <div className="mt-0.5 truncate text-sm text-gray-700">
          {result.company}
          {result.location_text && (
            <>
              <span className="mx-1.5 text-gray-300">·</span>
              <span className="text-gray-500">{result.location_text}</span>
            </>
          )}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
          {result.category && (
            <span className="rounded-full bg-gray-100 px-2 py-0.5 text-gray-700">
              {categoryLabel(result.category)}
            </span>
          )}
          {result.remote_type && (
            <span className="rounded-full bg-gray-100 px-2 py-0.5 text-gray-700">
              {REMOTE_LABELS[result.remote_type] ?? result.remote_type}
            </span>
          )}
          {money && (
            <span className="font-medium text-emerald-700">{money}</span>
          )}
        </div>
      </div>
      <time className="shrink-0 whitespace-nowrap text-xs text-gray-400">
        {timeAgo(result.posted_at)}
      </time>
    </div>
  );

  return (
    <li className="group border-b border-gray-100 last:border-b-0">
      {hasSlug ? (
        <a
          href={href}
          className="block px-4 py-4 transition-colors hover:bg-gray-50 focus-visible:bg-gray-50 focus-visible:outline-none"
        >
          {Inner}
        </a>
      ) : (
        <div className="px-4 py-4 opacity-75" aria-label="Job listing (no link available)">
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
      className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md bg-gradient-to-br from-navy-900 to-navy-700 text-sm font-semibold text-white"
      aria-hidden="true"
    >
      {initial}
    </div>
  );
}
