import { useCallback, useEffect, useState } from "react";
import {
  applyToOpportunity,
  fetchOpportunities,
  starOpportunity,
  unstarOpportunity,
  type FeedItem,
  type OpportunityFilter,
} from "@/api/candidates";
import { OpportunityCard } from "./OpportunityCard";

const FILTERS: { id: OpportunityFilter; label: string }[] = [
  { id: "all",     label: "All"      },
  { id: "matches", label: "Matches"  },
  { id: "starred", label: "Starred"  },
  { id: "applied", label: "Applied"  },
];

function readFilterFromURL(): OpportunityFilter {
  if (typeof window === "undefined") return "all";
  const v = new URL(window.location.href).searchParams.get("filter");
  if (v === "matches" || v === "starred" || v === "applied") return v;
  return "all";
}

function writeFilterToURL(filter: OpportunityFilter) {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  if (filter === "all") url.searchParams.delete("filter");
  else url.searchParams.set("filter", filter);
  window.history.pushState({}, "", url.toString());
}

export function OpportunitiesFeed() {
  const [filter, setFilter] = useState<OpportunityFilter>(readFilterFromURL);
  const [items, setItems] = useState<FeedItem[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (f: OpportunityFilter, cursor?: string) => {
    setLoading(true);
    setError(null);
    try {
      const page = await fetchOpportunities({ filter: f, cursor });
      setItems((prev) => (cursor ? [...prev, ...page.items] : page.items));
      setNextCursor(page.next_cursor);
    } catch {
      setError("Couldn't load your opportunities. Refresh in a few seconds — if this keeps happening, drop us a line at jobs@stawi.org.");
    } finally {
      setLoading(false);
    }
  }, []);

  // Initial load + on filter change.
  useEffect(() => { void load(filter); }, [filter, load]);

  const onSelectFilter = (id: OpportunityFilter) => {
    if (id === filter) return;
    writeFilterToURL(id);
    setFilter(id);
  };

  const onStar = useCallback(async (id: string) => {
    const snapshot = items;
    setItems((prev) => prev.map((it) => (it.opportunity_id === id ? { ...it, starred: true } : it)));
    try {
      await starOpportunity(id);
    } catch {
      setItems(snapshot);
    }
  }, [items]);

  const onUnstar = useCallback(async (id: string) => {
    const snapshot = items;
    setItems((prev) => prev.map((it) => (it.opportunity_id === id ? { ...it, starred: false } : it)));
    try {
      await unstarOpportunity(id);
    } catch {
      setItems(snapshot);
    }
  }, [items]);

  const onApply = useCallback(async (id: string) => {
    const snapshot = items;
    const now = new Date().toISOString();
    setItems((prev) =>
      prev.map((it) =>
        it.opportunity_id === id
          ? { ...it, application: { status: "applied", applied_at: now, last_event_at: now, method: "manual" } }
          : it,
      ),
    );
    try {
      await applyToOpportunity(id, "manual");
    } catch {
      setItems(snapshot);
    }
  }, [items]);

  return (
    <section aria-label="Your opportunities" className="space-y-4">
      <div className="flex flex-wrap items-center gap-2" role="tablist">
        {FILTERS.map((f) => {
          const active = f.id === filter;
          return (
            <button
              key={f.id}
              role="tab"
              aria-selected={active}
              type="button"
              onClick={() => onSelectFilter(f.id)}
              className={`rounded-full px-3.5 py-1.5 text-sm font-medium transition-colors ${
                active
                  ? "bg-navy-900 text-white"
                  : "border border-gray-300 bg-white text-gray-700 hover:bg-gray-50"
              }`}
            >
              {f.label}
            </button>
          );
        })}
      </div>

      {error ? (
        <div role="alert" className="rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800">
          {error}
        </div>
      ) : loading && items.length === 0 ? (
        <p className="rounded-md border border-gray-200 bg-white p-4 text-sm text-gray-600">Loading…</p>
      ) : items.length === 0 ? (
        <p className="rounded-md border border-gray-200 bg-white p-4 text-sm text-gray-600">
          Nothing to show here yet. {filter !== "all" && "Try the 'All' filter."}
        </p>
      ) : (
        <>
          <ul className="space-y-3">
            {items.map((it) => (
              <OpportunityCard
                key={it.opportunity_id}
                item={it}
                snapshot={null}
                onStar={onStar}
                onUnstar={onUnstar}
                onApply={onApply}
              />
            ))}
          </ul>
          {nextCursor && (
            <button
              type="button"
              onClick={() => void load(filter, nextCursor)}
              className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              disabled={loading}
            >
              {loading ? "Loading…" : "Load more"}
            </button>
          )}
        </>
      )}
    </section>
  );
}
