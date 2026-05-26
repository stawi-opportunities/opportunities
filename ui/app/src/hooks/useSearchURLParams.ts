import { useEffect, useState } from "react";
import type { SearchParams } from "@/types/search";

// The keys we persist to / read from the URL query string.
// Numeric params (limit, offset, cursor) are intentionally omitted —
// they're runtime-only pagination state, not shareable filter state.
const URL_KEYS = [
  "q",
  "category",
  "remote_type",
  "employment_type",
  "seniority",
  "country",
  "sort",
] as const satisfies ReadonlyArray<keyof SearchParams>;

function readFromURL(): SearchParams {
  if (typeof window === "undefined") return {};
  const p = new URL(window.location.href).searchParams;
  const out: SearchParams = {};
  for (const k of URL_KEYS) {
    const v = p.get(k);
    if (v) (out as Record<string, unknown>)[k] = v;
  }
  return out;
}

function writeToURL(params: SearchParams): void {
  const url = new URL(window.location.href);
  for (const k of URL_KEYS) {
    const v = params[k];
    if (v) url.searchParams.set(k, String(v));
    else url.searchParams.delete(k);
  }
  window.history.replaceState({}, "", url.toString());
}

/**
 * Synchronises a SearchParams state object with the browser URL.
 *
 * - Initialises from the current URL on mount so deep links and
 *   back-button navigations restore filter state.
 * - Writes changes back via `history.replaceState` (no full navigation)
 *   so the URL stays shareable without adding history entries.
 *
 * Returns `[params, setParams]` — the same tuple shape as `useState`.
 */
export function useSearchURLParams(): [
  SearchParams,
  (next: SearchParams) => void,
] {
  const [params, setParamsState] = useState<SearchParams>(() => readFromURL());

  useEffect(() => {
    writeToURL(params);
  }, [params]);

  return [params, setParamsState];
}
