/**
 * Related / similar opportunities under a listing detail body.
 */

import { useQuery } from '@tanstack/react-query';
import { fetchRelated } from '@/api/related';
import type { OpportunitySnapshot } from '@/types/snapshot';
import type { SearchResult } from '@/types/search';
import { getTypeMeta } from '@/constants/opportunityTypes';
import { Icon } from '@/components/ui/Icon';
import { timeAgo } from '@/utils/format';

function hrefFor(r: SearchResult): string {
  const kind = (r.kind || 'job').toLowerCase();
  const prefix =
    kind === 'scholarship'
      ? 'scholarships'
      : kind === 'tender'
        ? 'tenders'
        : kind === 'deal'
          ? 'deals'
          : kind === 'funding'
            ? 'funding'
            : 'jobs';
  return `/${prefix}/${encodeURIComponent(r.slug)}/`;
}

function locationLine(r: SearchResult): string {
  if (r.location_text) return r.location_text;
  if (r.country) return r.country;
  if (r.remote_type && r.remote_type !== 'onsite') return r.remote_type;
  return '';
}

export function RelatedOpportunities({
  snap,
  limit = 8,
}: {
  snap: OpportunitySnapshot;
  limit?: number;
}) {
  const q = useQuery({
    queryKey: ['related', snap.slug, limit],
    queryFn: () => fetchRelated(snap.slug, limit),
    staleTime: 5 * 60_000,
    enabled: !!snap.slug,
  });

  const items = q.data ?? [];
  if (q.isLoading) {
    return (
      <section className="mt-14 border-t border-slate-200 pt-10" aria-busy="true">
        <h2 className="text-lg font-semibold text-slate-900">Similar opportunities</h2>
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className="h-24 animate-pulse rounded-xl bg-slate-100" />
          ))}
        </div>
      </section>
    );
  }
  if (!items.length) return null;

  const label = snap.kind === 'job' || !snap.kind ? 'Similar jobs' : 'Related opportunities';

  return (
    <section className="mt-14 border-t border-slate-200 pt-10" aria-labelledby="related-heading">
      <div className="flex items-end justify-between gap-3">
        <div>
          <h2 id="related-heading" className="text-lg font-semibold text-slate-900">
            {label}
          </h2>
          <p className="mt-1 text-sm text-slate-600">
            Based on this listing’s role, type, and market.
          </p>
        </div>
      </div>
      <ul className="mt-5 grid gap-3 sm:grid-cols-2">
        {items.map((r) => {
          const meta = r.kind ? getTypeMeta(r.kind) : null;
          return (
            <li key={r.slug}>
              <a
                href={hrefFor(r)}
                className="group flex h-full flex-col rounded-xl border border-slate-200 bg-white p-4 shadow-sm transition hover:border-indigo-300 hover:shadow-md"
              >
                <div className="flex items-start gap-2">
                  <div className="min-w-0 flex-1">
                    <p className="line-clamp-2 text-sm font-semibold text-slate-900 group-hover:text-indigo-700">
                      {r.title}
                    </p>
                    <p className="mt-1 truncate text-xs text-slate-600">
                      {r.company || r.issuing_entity || ''}
                    </p>
                  </div>
                  {meta && (
                    <span className="shrink-0 rounded-full bg-slate-100 p-1.5 text-slate-500">
                      <Icon name={meta.iconName} size={12} />
                    </span>
                  )}
                </div>
                <div className="mt-3 flex flex-wrap gap-x-2 gap-y-1 text-xs text-slate-500">
                  {locationLine(r) && <span>{locationLine(r)}</span>}
                  {r.remote_type && r.remote_type !== 'onsite' && (
                    <span className="rounded-full bg-slate-100 px-1.5 py-0.5">{r.remote_type}</span>
                  )}
                  {r.posted_at && <span>{timeAgo(r.posted_at)}</span>}
                </div>
              </a>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
