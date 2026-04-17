// api.js — shared fetch helpers for R2 and the search API.
//
// Origins are read from <meta> tags injected by Hugo from hugo.toml params.
// This avoids hardcoding per-environment URLs in the JS bundle.

const meta = (name) =>
  document.querySelector(`meta[name="${name}"]`)?.getAttribute("content") || "";

export const CONTENT_ORIGIN =
  meta("content-origin") || "https://content.stawi.jobs";
export const API_ORIGIN = meta("api-origin") || "";

/** GET a JobSnapshot directly from R2. Returns null on 404. */
export async function fetchSnapshot(slug) {
  const res = await fetch(
    `${CONTENT_ORIGIN}/jobs/${encodeURIComponent(slug)}.json`,
    { credentials: "omit" },
  );
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`snapshot ${slug}: HTTP ${res.status}`);
  return res.json();
}

export async function fetchCategoriesIndex() {
  try {
    const res = await fetch(`${CONTENT_ORIGIN}/categories/index.json`, {
      credentials: "omit",
    });
    if (res.ok) return res.json();
  } catch (_) {}
  // Fallback: ask the API. This keeps the home page working even before the
  // R2 categories/index.json file is populated.
  const res = await fetch(apiURL("/api/categories"), { credentials: "include" });
  if (!res.ok) return { categories: [] };
  return res.json();
}

function apiURL(path, params = {}) {
  const origin = API_ORIGIN || window.location.origin;
  const u = new URL(path, origin);
  for (const [k, v] of Object.entries(params)) {
    if (v !== "" && v != null) u.searchParams.set(k, v);
  }
  return u.toString();
}

export async function apiGet(path, params = {}) {
  const res = await fetch(apiURL(path, params), { credentials: "include" });
  if (!res.ok) throw new Error(`${path}: HTTP ${res.status}`);
  return res.json();
}
