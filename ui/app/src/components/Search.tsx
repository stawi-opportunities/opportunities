import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { searchJobs } from "@/api/search";
import { JobRow } from "./JobRow";
import type { FacetEntry, SearchParams } from "@/types/search";

/**
 * /search/ — query + filters + facets + pagination. Reads initial state
 * from the URL (so deep links share-able) and writes back to the URL on
 * changes using history.replaceState (so the browser back button stays
 * predictable without full navigations).
 */
export default function Search() {
  const [params, setParams] = useState<SearchParams>(() => readParamsFromURL());
  useEffect(() => writeParamsToURL(params), [params]);

  const q = useQuery({
    queryKey: ["search", params],
    queryFn: () => searchJobs({ ...params, limit: 25 }),
    staleTime: 15_000,
  });

  const facets = q.data?.facets;

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <header className="mb-6">
        <SearchForm
          value={params}
          onChange={(next) => setParams(next)}
        />
      </header>

      <div className="grid gap-8 md:grid-cols-[240px_1fr]">
        <aside>
          <SortPicker
            value={params.sort ?? (params.q ? "relevance" : "recent")}
            onChange={(sort) => setParams({ ...params, sort })}
          />
          {facets && (
            <>
              <FacetBlock label="Category"        entries={facets.category}        selected={params.category} onSelect={(v) => setParams({ ...params, category: v })} />
              <FacetBlock label="Remote"          entries={facets.remote_type}     selected={params.remote_type} onSelect={(v) => setParams({ ...params, remote_type: v })} />
              <FacetBlock label="Employment type" entries={facets.employment_type} selected={params.employment_type} onSelect={(v) => setParams({ ...params, employment_type: v })} />
              <FacetBlock label="Seniority"       entries={facets.seniority}       selected={params.seniority} onSelect={(v) => setParams({ ...params, seniority: v })} />
              <FacetBlock label="Country"         entries={facets.country}         selected={params.country} onSelect={(v) => setParams({ ...params, country: v })} />
            </>
          )}
        </aside>

        <section>
          {q.isLoading && <Skeleton />}
          {q.isError && <p className="text-red-700">Search failed. Try again.</p>}
          {q.data && (
            <>
              <p className="mb-4 text-sm text-gray-500">
                {q.data.results.length} shown
                {q.data.has_more ? " · more available" : ""}
              </p>
              <ul className="divide-y divide-gray-200 rounded-lg border border-gray-200">
                {q.data.results.map((r) => (
                  <JobRow key={r.id} result={r} />
                ))}
                {q.data.results.length === 0 && (
                  <li className="px-4 py-6 text-center text-sm text-gray-500">
                    No jobs match. Try fewer filters.
                  </li>
                )}
              </ul>
              {q.data.has_more && (
                <div className="mt-6 text-center">
                  <button
                    type="button"
                    className="rounded border border-gray-300 px-4 py-2 text-sm hover:bg-gray-50"
                    onClick={() =>
                      setParams({
                        ...params,
                        offset: (params.offset ?? 0) + 25,
                      })
                    }
                  >
                    Load more
                  </button>
                </div>
              )}
            </>
          )}
        </section>
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
    >
      <input
        type="search"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Search by title, skill, or company…"
        className="flex-1 rounded-lg border border-gray-300 px-4 py-2"
        aria-label="Search jobs"
      />
      <button type="submit" className="rounded-lg bg-navy-900 px-5 py-2 text-white hover:bg-navy-800">
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
    <div className="mb-6">
      <label className="block text-xs font-semibold uppercase tracking-wider text-gray-500">
        Sort
      </label>
      <select
        value={value ?? ""}
        onChange={(e) => onChange(e.target.value as SearchParams["sort"])}
        className="mt-1 w-full rounded border border-gray-300 px-2 py-1 text-sm"
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
  onSelect,
}: {
  label: string;
  entries: FacetEntry[];
  selected: string | undefined;
  onSelect: (v: string | undefined) => void;
}) {
  if (!entries.length) return null;
  return (
    <div className="mb-4">
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-gray-500">
        {label}
      </h3>
      <ul>
        {entries.slice(0, 8).map((e) => {
          const isSel = selected === e.key;
          return (
            <li key={e.key} className="py-0.5">
              <button
                type="button"
                className={`flex w-full items-center justify-between rounded px-2 py-1 text-left text-sm hover:bg-gray-50 ${
                  isSel ? "bg-blue-50 text-blue-800" : "text-gray-700"
                }`}
                onClick={() => onSelect(isSel ? undefined : e.key)}
              >
                <span className="capitalize">{e.key || "(uncategorised)"}</span>
                <span className="text-xs text-gray-400">{e.count.toLocaleString()}</span>
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
      {Array.from({ length: 5 }).map((_, i) => (
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
