import { useCallback, useEffect, useMemo, useRef } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { feed, loadTierPage } from "@/api/search";
import { fetchManifest } from "@/api/manifest";
import type {
  Facets,
  FeedParams,
  FeedResponse,
  FeedTier,
  SearchResult,
  TierPageParams,
} from "@/types/search";
import { JobRow } from "./JobRow";
import {
  buildBoost,
  countryLabel,
  getVisitorLocale,
} from "@/utils/locale";

// Cascade is the tiered-feed view. Renders whatever sections the
// server returned in order (preferred → local → regional → global),
// with per-section "load more" driven by the tier's cursor so each
// section paginates independently.
//
// Snappiness strategy:
//   - first render returns the full cascade in one request (parallel
//     fan-out server-side)
//   - filter / search-string changes re-run the root query with the
//     React-Query cache keeping the previous data on screen so the
//     user never sees a blank list mid-edit
//   - "Load more" prefetches the next page 400px before the button
//     enters the viewport via IntersectionObserver
//   - preferences (signed-in) land as `boost_*` params on the root
//     query; CF-IPCountry / Accept-Language drive the server fallback.
export interface CascadeProps {
  /** Common filters that apply within every tier (category / seniority
   *  / employment / remote / text query). Changing these re-fetches. */
  filters?: {
    q?: string;
    category?: string;
    remote_type?: string;
    employment_type?: string;
    seniority?: string;
    sort?: FeedParams["sort"];
  };
  /** Logged-in user's declared preferences. Empty arrays → no
   *  preferred tier, cascade begins with "local". */
  preferredCountries?: string[];
  preferredLanguages?: string[];
  /** Jobs per section in the initial cascade (default 25). */
  tierLimit?: number;
  /** Callback so parent can surface the context (e.g. "Showing jobs
   *  near Kenya · [change]"). */
  onContextChange?: (country: string, languages: string[]) => void;
  /** Callback emitting facet counts once the feed response lands —
   *  lets the Search page keep filter checkbox counts without a
   *  duplicate /api/search call. */
  onFacets?: (facets: Facets) => void;
  /** Explicit override for the country used in the feed request.
   *  Set by per-country landing pages (/l/KE/) so the cascade
   *  ignores the visitor's detected locale on pages that are
   *  dedicated to a specific country. */
  overrideCountry?: string;
  /** Explicit override for the language list used in the feed
   *  request. Same rationale as overrideCountry. */
  overrideLanguages?: string[];
}

export default function Cascade(props: CascadeProps) {
  const {
    filters = {},
    preferredCountries = [],
    preferredLanguages = [],
    tierLimit = 25,
    overrideCountry,
    overrideLanguages,
  } = props;

  // Resolve visitor locale once per render; the heavy lifting is
  // memoised inside getVisitorLocale(). Per-page overrides win so
  // /l/KE/ doesn't cascade "near the visitor" — it cascades near KE.
  const detected = useMemo(getVisitorLocale, []);
  const effectiveCountry = overrideCountry ?? detected.country;
  const effectiveLanguages = overrideLanguages ?? detected.languages;

  const feedParams = useMemo<FeedParams>(() => {
    const p: FeedParams = {
      ...filters,
      tier_limit: tierLimit,
    };
    if (effectiveCountry) p.country = effectiveCountry;
    if (effectiveLanguages.length) p.lang = effectiveLanguages.join(",");
    const boost = buildBoost(preferredCountries.join(","), preferredLanguages.join(","));
    if (boost.countries.length) p.boost_countries = boost.countries.join(",");
    if (boost.languages.length) p.boost_languages = boost.languages.join(",");
    return p;
  }, [filters, tierLimit, effectiveCountry, effectiveLanguages, preferredCountries, preferredLanguages]);

  // canUseManifest: true when the request is vanilla enough to be
  // served from a pre-baked R2 manifest. Any filter, any boost, or
  // a text query means the manifest can't match — the live API
  // takes over in those cases.
  const canUseManifest = useMemo(() => {
    return (
      !feedParams.q &&
      !feedParams.category &&
      !feedParams.remote_type &&
      !feedParams.employment_type &&
      !feedParams.seniority &&
      !feedParams.boost_countries &&
      !feedParams.boost_languages &&
      (!feedParams.sort || feedParams.sort === "recent")
    );
  }, [feedParams]);

  const q = useQuery<FeedResponse>({
    // The key includes canUseManifest so changing source (manifest ↔
    // live API) is a fresh fetch rather than stale-data reuse.
    queryKey: ["feed", feedParams, canUseManifest ? "r2" : "api"],
    queryFn: async () => {
      if (canUseManifest) {
        // Try R2 first. A null fallback kicks the API path.
        const m = await fetchManifest(effectiveCountry);
        if (m) return m;
      }
      return feed(feedParams);
    },
    // Match the R2 cache-control (s-maxage=300). Live-API hits get
    // to use the same staleness budget — if the user toggles a
    // filter we still serve from cache first, re-fetch in background.
    staleTime: 5 * 60_000,
    // Keep previous data while a new fetch is in-flight so filter
    // toggles don't flash a skeleton.
    placeholderData: (prev) => prev,
  });

  useEffect(() => {
    if (q.data?.context && props.onContextChange) {
      props.onContextChange(q.data.context.country, q.data.context.languages);
    }
  }, [q.data?.context.country, q.data?.context.languages, props.onContextChange, q.data?.context]);

  useEffect(() => {
    if (q.data?.facets && props.onFacets) {
      props.onFacets(q.data.facets);
    }
  }, [q.data?.facets, props.onFacets]);

  if (q.isLoading && !q.data) return <CascadeSkeleton />;
  if (q.isError) {
    return (
      <p className="mt-6 text-red-700">
        Unable to load jobs right now. Please try again in a moment.
      </p>
    );
  }
  if (!q.data) return null;

  if (q.data.tiers.length === 0) {
    return (
      <p className="mt-8 text-center text-sm text-gray-600">
        No jobs match those filters yet. Try widening your search.
      </p>
    );
  }

  return (
    <div className="mt-2 space-y-10">
      {q.data.tiers.map((tier) => (
        <TierSection
          key={tier.id}
          tier={tier}
          filters={filters}
          effectiveCountry={effectiveCountry}
        />
      ))}
    </div>
  );
}

function TierSection({
  tier,
  filters,
  effectiveCountry,
}: {
  tier: FeedTier;
  filters: NonNullable<CascadeProps["filters"]>;
  effectiveCountry: string;
}) {
  const pageParams = useMemo<TierPageParams>(() => {
    return {
      q: filters.q,
      category: filters.category,
      remote_type: filters.remote_type,
      employment_type: filters.employment_type,
      seniority: filters.seniority,
      sort: filters.sort,
      country: tier.country ?? "",
      countries: tier.countries?.join(",") ?? "",
      language: tier.language ?? "",
      limit: 25,
    };
  }, [tier, filters]);

  const infinite = useInfiniteQuery({
    queryKey: ["feed-tier", tier.id, pageParams, tier.cursor],
    enabled: false, // fetchNextPage() kicks this off; initial data seeded from parent.
    initialPageParam: "" as string,
    // `getNextPageParam` returns the cursor to fetch *next*, or
    // undefined to mean "no more". Pagination model: cursor points at
    // the posted_at/id pair of the last item in the previous page.
    getNextPageParam: (last: { cursor_next: string; has_more: boolean }) =>
      last.has_more ? last.cursor_next : undefined,
    queryFn: ({ pageParam }) =>
      loadTierPage({ ...pageParams, cursor: pageParam || undefined }),
    initialData: {
      pages: [{ jobs: tier.jobs, cursor_next: tier.cursor, has_more: tier.has_more }],
      pageParams: [""],
    },
  });

  // All jobs fetched for this tier, flattened from the pages.
  const allJobs: SearchResult[] = useMemo(() => {
    const pages = infinite.data?.pages ?? [];
    const seen = new Set<number>();
    const out: SearchResult[] = [];
    for (const page of pages) {
      for (const j of page.jobs) {
        // Defensive: the server de-dupes across tiers via
        // ExcludeCountries, but an unlucky overlap in cursors could
        // duplicate inside one tier if a job's posted_at ticks. Keep
        // a tiny set as guard.
        if (!seen.has(j.id)) {
          seen.add(j.id);
          out.push(j);
        }
      }
    }
    return out;
  }, [infinite.data]);

  const lastPage = infinite.data?.pages?.[infinite.data.pages.length - 1];
  const hasMore = lastPage?.has_more ?? false;

  return (
    <section aria-labelledby={`tier-${tier.id}`}>
      <header className="flex items-baseline justify-between border-b border-gray-200 pb-2">
        <h2
          id={`tier-${tier.id}`}
          className="text-lg font-semibold text-navy-900"
        >
          {tier.label}
        </h2>
        <TierScopeNote tier={tier} effectiveCountry={effectiveCountry} />
      </header>

      <ul className="mt-3 divide-y divide-gray-200 rounded-lg border border-gray-200">
        {allJobs.map((j) => (
          <JobRow key={`${tier.id}-${j.id}`} result={j} />
        ))}
      </ul>

      {hasMore && (
        <PrefetchButton
          onNear={() => infinite.fetchNextPage()}
          busy={infinite.isFetchingNextPage}
        />
      )}
    </section>
  );
}

function TierScopeNote({
  tier,
  effectiveCountry,
}: {
  tier: FeedTier;
  effectiveCountry: string;
}) {
  // A short secondary label so the user knows exactly what filter
  // this section represents — avoids "why is 'Local' showing Nigeria
  // jobs?" when CF mis-geo's them.
  const parts: string[] = [];
  if (tier.id === "preferred") parts.push("Your preferences");
  if (tier.country) parts.push(countryLabel(tier.country));
  if (tier.language) parts.push(tier.language.toUpperCase());
  if (!parts.length && tier.id === "global") {
    parts.push(effectiveCountry ? `Outside ${countryLabel(effectiveCountry)}` : "Worldwide");
  }
  if (!parts.length) return null;
  return (
    <span className="text-xs uppercase tracking-wide text-gray-500">
      {parts.join(" · ")}
    </span>
  );
}

function PrefetchButton({ onNear, busy }: { onNear: () => void; busy: boolean }) {
  const ref = useRef<HTMLButtonElement | null>(null);
  const fired = useRef(false);

  // Prefetch ~400px before the button enters the viewport so the
  // user experiences "infinite scroll" rather than "press, wait,
  // read". The ref-guard prevents the observer from double-firing
  // during the network round-trip.
  const handleIntersect = useCallback<IntersectionObserverCallback>(
    (entries) => {
      for (const e of entries) {
        if (e.isIntersecting && !fired.current) {
          fired.current = true;
          onNear();
          // Reset the guard after the fetch settles so subsequent
          // scroll-down triggers fire again.
          setTimeout(() => { fired.current = false; }, 2000);
        }
      }
    },
    [onNear],
  );

  useEffect(() => {
    if (!ref.current) return;
    const el = ref.current;
    const obs = new IntersectionObserver(handleIntersect, {
      rootMargin: "400px 0px",
    });
    obs.observe(el);
    return () => obs.disconnect();
  }, [handleIntersect]);

  return (
    <div className="mt-4 text-center">
      <button
        ref={ref}
        type="button"
        onClick={() => onNear()}
        disabled={busy}
        className="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60"
      >
        {busy ? "Loading…" : "Load more"}
      </button>
    </div>
  );
}

function CascadeSkeleton() {
  // Shows structure (3 sections) while the first /api/feed fetch is
  // in flight — less jarring than a single tall spinner because the
  // real content lands into the same shapes.
  return (
    <div className="mt-2 space-y-10">
      {[0, 1, 2].map((i) => (
        <section key={i}>
          <div className="h-5 w-40 rounded bg-gray-200" />
          <ul className="mt-3 space-y-px rounded-lg border border-gray-200">
            {Array.from({ length: 5 }).map((_, k) => (
              <li key={k} className="h-20 animate-pulse bg-gray-100" />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

// Re-export for pages that want to render just the cascade in a page
// layout without prop drilling.
export { TierSection as CascadeSection };
