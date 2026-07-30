import type { SearchResult } from '@/types/search';

export type RelatedResponse = {
  source_slug: string;
  count: number;
  results: SearchResult[];
};

/**
 * GET /opportunities/api/opportunities/{slug}/related — similar listings.
 * Gateway may strip prefixes; discovery API is under /opportunities after CF.
 */
export async function fetchRelated(
  slug: string,
  limit = 8
): Promise<SearchResult[]> {
  const base =
    (typeof import.meta !== 'undefined' &&
      (import.meta as { env?: { VITE_API_BASE?: string } }).env?.VITE_API_BASE) ||
    '';
  const path = `/opportunities/api/opportunities/${encodeURIComponent(slug)}/related?limit=${limit}`;
  const url = base ? `${base.replace(/\/$/, '')}${path}` : path;
  try {
    const res = await fetch(url, {
      headers: { Accept: 'application/json' },
      credentials: 'omit',
    });
    if (!res.ok) return [];
    const data = (await res.json()) as RelatedResponse;
    return Array.isArray(data.results) ? data.results : [];
  } catch {
    return [];
  }
}
