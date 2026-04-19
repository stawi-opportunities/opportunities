// R2 feed-manifest client.
//
// The Go service writes per-country manifests to R2 every 3h (via
// POST /admin/feeds/rebuild). The Cascade tries R2 first for unfiltered
// anonymous / lightly-personalised requests — it's a single CDN-cached
// static file, so first paint is fast and doesn't depend on the job
// API being up.
//
// Callers still fall back to /api/feed for any request the manifest
// can't cover: text search, filter combinations, logged-in preference
// boosts, or a 404 (manifest not yet published for the user's shard).

import { getConfig } from "@/utils/config";
import type { FeedResponse } from "@/types/search";

/** One entry in /feeds/index.json — lets the client probe staleness
 *  and verify a given country has a published manifest before hitting
 *  the shard file. Mirrors apps/api/cmd/manifest.go:indexShardRef. */
export interface ManifestIndexEntry {
  country: string;
  languages: string[] | null;
  key: string;
  updated_at: string;
}

export interface ManifestIndex {
  generated_at: string;
  shards: ManifestIndexEntry[];
}

/** Full manifest (same shape as /api/feed + a generated_at stamp).
 *  Mirrors apps/api/cmd/manifest.go:feedManifest. */
export interface Manifest extends FeedResponse {
  generated_at: string;
}

function manifestURL(path: string): string {
  const origin = getConfig().contentOrigin.replace(/\/$/, "");
  return `${origin}/${path.replace(/^\/+/, "")}`;
}

/**
 * Fetch a country's pre-baked manifest. Returns null on 404 / network
 * error so the caller can cleanly fall back to the live API.
 *
 * `country` is the ISO-3166 alpha-2 (case-insensitive). Pass empty
 * string to fetch `/feeds/default.json`.
 */
export async function fetchManifest(country: string): Promise<Manifest | null> {
  const key = country
    ? `feeds/${country.toLowerCase()}.json`
    : "feeds/default.json";
  try {
    const res = await fetch(manifestURL(key), {
      // No credentials — R2 is public, we don't want cookies forwarded.
      credentials: "omit",
      // Respect the server's Cache-Control (max-age=60, s-maxage=300).
      cache: "default",
    });
    if (!res.ok) return null;
    return (await res.json()) as Manifest;
  } catch {
    return null;
  }
}

/**
 * Index fetch — used by diagnostics / a future "data is N min old"
 * indicator. Null on failure so callers can treat it as optional.
 */
export async function fetchManifestIndex(): Promise<ManifestIndex | null> {
  try {
    const res = await fetch(manifestURL("feeds/index.json"), {
      credentials: "omit",
      cache: "default",
    });
    if (!res.ok) return null;
    return (await res.json()) as ManifestIndex;
  } catch {
    return null;
  }
}
