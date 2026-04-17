import type { SearchResult } from "@/types/search";
import { fmtMoney, timeAgo } from "@/utils/format";

/** Shared row renderer used by search results, category lists, home feed. */
export function JobRow({ result }: { result: SearchResult }) {
  const money = fmtMoney(result.salary_min, result.salary_max, result.currency);
  const hasSlug = !!result.slug;
  const href = hasSlug ? `/jobs/${encodeURIComponent(result.slug)}/` : undefined;
  return (
    <li className="px-4 py-3">
      <a
        href={href}
        aria-disabled={!hasSlug}
        onClick={hasSlug ? undefined : (e) => e.preventDefault()}
        className={hasSlug ? "block hover:bg-slate-50" : "block cursor-default opacity-80"}
      >
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="font-semibold text-gray-900 truncate">{result.title}</span>
              {result.is_featured && (
                <span className="rounded-full bg-accent-50 px-2 py-0.5 text-xs font-medium text-accent-700">
                  Featured
                </span>
              )}
            </div>
            <div className="mt-1 text-sm text-gray-600 truncate">
              {result.company}
              {result.location_text && <span className="text-gray-400"> · {result.location_text}</span>}
            </div>
            <div className="mt-1 flex flex-wrap gap-2 text-xs text-gray-500">
              {result.category && <span className="capitalize">{result.category}</span>}
              {result.remote_type && <span>· {result.remote_type}</span>}
              {money && <span>· {money}</span>}
              <span className="ml-auto">{timeAgo(result.posted_at)}</span>
            </div>
          </div>
        </div>
      </a>
    </li>
  );
}
