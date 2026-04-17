import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { searchJobs } from "@/api/search";
import { JobRow } from "./JobRow";
import { categoryLabel } from "@/utils/format";
import type { FacetEntry, Facets, SearchParams } from "@/types/search";

const REMOTE_FACET_LABELS: Record<string, string> = {
  remote: "Remote",
  hybrid: "Hybrid",
  onsite: "On-site",
  on_site: "On-site",
};

/**
 * /search/ — query + filters + facets + pagination. Reads initial state
 * from the URL so deep links are shareable; writes back on changes via
 * history.replaceState so the back button stays predictable without full
 * navigations.
 */
export default function Search() {
  const [params, setParams] = useState<SearchParams>(() => readParamsFromURL());
  const [filtersOpen, setFiltersOpen] = useState(false);
  useEffect(() => writeParamsToURL(params), [params]);

  const q = useQuery({
    queryKey: ["search", params],
    queryFn: () => searchJobs({ ...params, limit: 25 }),
    staleTime: 15_000,
  });

  const facets = q.data?.facets;
  const hasActiveFilters = Boolean(
    params.category ||
      params.remote_type ||
      params.employment_type ||
      params.seniority ||
      params.country ||
      params.q,
  );

  function clearAll() {
    setParams({ sort: params.sort });
  }

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <header className="mb-6">
        <h1 className="sr-only">Search jobs</h1>
        <SearchForm
          value={params}
          onChange={(next) => setParams(next)}
        />
      </header>

      <div className="flex items-center justify-between border-b border-gray-200 pb-3 md:hidden">
        <button
          type="button"
          onClick={() => setFiltersOpen(true)}
          className="inline-flex items-center gap-2 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 shadow-sm"
        >
          <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.172a2 2 0 01-.586 1.414l-5.828 5.828A2 2 0 0013 14.828V17l-4 4v-6.172a2 2 0 00-.586-1.414L2.586 7.586A2 2 0 012 6.172V4z" />
          </svg>
          Filters
          {hasActiveFilters && (
            <span className="rounded-full bg-navy-900 px-2 py-0.5 text-xs text-white">
              •
            </span>
          )}
        </button>
        {hasActiveFilters && (
          <button
            type="button"
            onClick={clearAll}
            className="text-sm text-gray-600 hover:text-gray-900"
          >
            Clear all
          </button>
        )}
      </div>

      <div className="grid gap-8 md:grid-cols-[260px_1fr]">
        <aside className="hidden md:block">
          <FiltersPanel
            params={params}
            setParams={setParams}
            facets={facets}
            hasActiveFilters={hasActiveFilters}
            onClear={clearAll}
          />
        </aside>

        <section>
          {q.isLoading && <Skeleton />}
          {q.isError && (
            <div
              className="rounded-md bg-red-50 p-4 text-sm text-red-700"
              role="alert"
            >
              We couldn't run your search.{" "}
              <button
                type="button"
                onClick={() => q.refetch()}
                className="font-medium underline hover:text-red-800"
              >
                Retry
              </button>
            </div>
          )}
          {q.data && (
            <>
              <p className="mb-4 text-sm text-gray-600">
                {q.data.results.length > 0
                  ? q.data.has_more
                    ? `Showing ${q.data.results.length.toLocaleString()} jobs · more available`
                    : `${q.data.results.length.toLocaleString()} ${
                        q.data.results.length === 1 ? "job" : "jobs"
                      }`
                  : ""}
              </p>
              <ul className="overflow-hidden rounded-lg border border-gray-200 bg-white">
                {q.data.results.map((r) => (
                  <JobRow key={r.id} result={r} />
                ))}
                {q.data.results.length === 0 && (
                  <li className="px-4 py-10 text-center">
                    <p className="text-sm text-gray-700">
                      No jobs match these filters.
                    </p>
                    {hasActiveFilters && (
                      <button
                        type="button"
                        onClick={clearAll}
                        className="mt-3 inline-block text-sm font-medium text-navy-700 hover:text-navy-900"
                      >
                        Clear filters →
                      </button>
                    )}
                  </li>
                )}
              </ul>
              {q.data.has_more && (
                <div className="mt-6 text-center">
                  <button
                    type="button"
                    disabled={q.isFetching}
                    className="rounded-md border border-gray-300 bg-white px-5 py-2 text-sm font-medium text-gray-700 shadow-sm hover:bg-gray-50 disabled:opacity-60"
                    onClick={() =>
                      setParams({
                        ...params,
                        offset: (params.offset ?? 0) + 25,
                      })
                    }
                  >
                    {q.isFetching ? "Loading…" : "Load more"}
                  </button>
                </div>
              )}
            </>
          )}
        </section>
      </div>

      {filtersOpen && (
        <MobileDrawer onClose={() => setFiltersOpen(false)}>
          <FiltersPanel
            params={params}
            setParams={(next) => {
              setParams(next);
            }}
            facets={facets}
            hasActiveFilters={hasActiveFilters}
            onClear={clearAll}
          />
          <div className="sticky bottom-0 mt-6 flex gap-3 border-t border-gray-200 bg-white px-4 py-3">
            <button
              type="button"
              onClick={clearAll}
              className="flex-1 rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700"
            >
              Clear all
            </button>
            <button
              type="button"
              onClick={() => setFiltersOpen(false)}
              className="flex-1 rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white"
            >
              Show results
            </button>
          </div>
        </MobileDrawer>
      )}
    </div>
  );
}

function FiltersPanel({
  params,
  setParams,
  facets,
  hasActiveFilters,
  onClear,
}: {
  params: SearchParams;
  setParams: (p: SearchParams) => void;
  facets: Facets | undefined;
  hasActiveFilters: boolean;
  onClear: () => void;
}) {
  return (
    <div className="md:sticky md:top-20">
      <div className="mb-4 flex items-center justify-between">
        <SortPicker
          value={params.sort ?? (params.q ? "relevance" : "recent")}
          onChange={(sort) => setParams({ ...params, sort })}
        />
        {hasActiveFilters && (
          <button
            type="button"
            onClick={onClear}
            className="hidden text-sm text-gray-600 hover:text-gray-900 md:inline"
          >
            Clear
          </button>
        )}
      </div>
      {facets && (
        <>
          <FacetBlock
            label="Category"
            entries={facets.category}
            selected={params.category}
            labeller={categoryLabel}
            onSelect={(v) => setParams({ ...params, category: v, offset: 0 })}
          />
          <FacetBlock
            label="Remote"
            entries={facets.remote_type}
            selected={params.remote_type}
            labeller={(k) => REMOTE_FACET_LABELS[k] ?? k}
            onSelect={(v) => setParams({ ...params, remote_type: v, offset: 0 })}
          />
          <FacetBlock
            label="Employment type"
            entries={facets.employment_type}
            selected={params.employment_type}
            onSelect={(v) =>
              setParams({ ...params, employment_type: v, offset: 0 })
            }
          />
          <FacetBlock
            label="Seniority"
            entries={facets.seniority}
            selected={params.seniority}
            onSelect={(v) => setParams({ ...params, seniority: v, offset: 0 })}
          />
          <FacetBlock
            label="Country"
            entries={facets.country}
            selected={params.country}
            onSelect={(v) => setParams({ ...params, country: v, offset: 0 })}
          />
        </>
      )}
    </div>
  );
}

function MobileDrawer({
  children,
  onClose,
}: {
  children: React.ReactNode;
  onClose: () => void;
}) {
  useEffect(() => {
    const onEsc = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onEsc);
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onEsc);
      document.body.style.overflow = "";
    };
  }, [onClose]);
  return (
    <div
      className="fixed inset-0 z-50 flex flex-col bg-black/40 md:hidden"
      role="dialog"
      aria-modal="true"
      aria-label="Filters"
      onClick={onClose}
    >
      <div
        className="mt-auto flex max-h-[92vh] flex-col overflow-hidden rounded-t-2xl bg-white"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-gray-200 px-4 py-3">
          <h2 className="text-base font-semibold text-gray-900">Filters</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close filters"
            className="rounded p-1 text-gray-500 hover:bg-gray-100"
          >
            <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
        <div className="flex-1 overflow-y-auto px-4 py-4">{children}</div>
      </div>
    </div>
  );
}

function SearchForm({
  value,
  onChange,
}: {
  value: SearchParams;
  onChange: (next: SearchParams) => void;
}) {
  const [q, setQ] = useState(value.q ?? "");
  useEffect(() => setQ(value.q ?? ""), [value.q]);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onChange({ ...value, q: q.trim() || undefined, offset: 0 });
      }}
      className="flex gap-2"
      role="search"
    >
      <div className="relative flex-1">
        <svg
          className="pointer-events-none absolute left-3 top-1/2 h-5 w-5 -translate-y-1/2 text-gray-400"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
          aria-hidden="true"
        >
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
        </svg>
        <input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search by title, skill, or company…"
          className="w-full rounded-md border border-gray-300 bg-white py-2.5 pl-10 pr-10 text-base shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
          aria-label="Search jobs"
        />
        {q && (
          <button
            type="button"
            onClick={() => {
              setQ("");
              onChange({ ...value, q: undefined, offset: 0 });
            }}
            aria-label="Clear search"
            className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-gray-400 hover:text-gray-600"
          >
            <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        )}
      </div>
      <button
        type="submit"
        className="rounded-md bg-navy-900 px-5 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
      >
        Search
      </button>
    </form>
  );
}

function SortPicker({
  value,
  onChange,
}: {
  value: SearchParams["sort"];
  onChange: (v: SearchParams["sort"]) => void;
}) {
  return (
    <div>
      <label className="block text-xs font-semibold uppercase tracking-wider text-gray-500">
        Sort
      </label>
      <select
        value={value ?? ""}
        onChange={(e) => onChange(e.target.value as SearchParams["sort"])}
        className="mt-1 rounded-md border border-gray-300 bg-white px-2 py-1 text-sm"
      >
        <option value="relevance">Relevance</option>
        <option value="recent">Most recent</option>
        <option value="quality">Highest quality</option>
        <option value="salary_high">Salary: high to low</option>
      </select>
    </div>
  );
}

function FacetBlock({
  label,
  entries,
  selected,
  labeller,
  onSelect,
}: {
  label: string;
  entries: FacetEntry[];
  selected: string | undefined;
  labeller?: (k: string) => string;
  onSelect: (v: string | undefined) => void;
}) {
  if (!entries.length) return null;
  const fmt = labeller ?? ((k: string) => k || "(uncategorised)");
  return (
    <div className="mb-5">
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-gray-500">
        {label}
      </h3>
      <ul>
        {entries.slice(0, 8).map((e) => {
          const isSel = selected === e.key;
          return (
            <li key={e.key}>
              <button
                type="button"
                aria-pressed={isSel}
                className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-sm transition-colors ${
                  isSel
                    ? "bg-navy-50 font-medium text-navy-900"
                    : "text-gray-700 hover:bg-gray-50"
                }`}
                onClick={() => onSelect(isSel ? undefined : e.key)}
              >
                <span>{fmt(e.key)}</span>
                <span className={`text-xs ${isSel ? "text-navy-700" : "text-gray-400"}`}>
                  {e.count.toLocaleString()}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Skeleton() {
  return (
    <ul className="space-y-2">
      {Array.from({ length: 6 }).map((_, i) => (
        <li key={i} className="h-20 animate-pulse rounded-lg bg-gray-100" />
      ))}
    </ul>
  );
}

function readParamsFromURL(): SearchParams {
  if (typeof window === "undefined") return {};
  const p = new URL(window.location.href).searchParams;
  const out: SearchParams = {};
  const pick = (k: keyof SearchParams) => {
    const v = p.get(k);
    if (v) (out as Record<string, unknown>)[k] = v;
  };
  pick("q");
  pick("category");
  pick("remote_type");
  pick("employment_type");
  pick("seniority");
  pick("country");
  pick("sort");
  return out;
}

function writeParamsToURL(params: SearchParams) {
  const url = new URL(window.location.href);
  for (const key of [
    "q",
    "category",
    "remote_type",
    "employment_type",
    "seniority",
    "country",
    "sort",
  ] as const) {
    const v = params[key];
    if (v) url.searchParams.set(key, String(v));
    else url.searchParams.delete(key);
  }
  window.history.replaceState({}, "", url.toString());
}
