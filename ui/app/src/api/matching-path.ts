/**
 * Gateway path prefix for the matching service.
 *
 * - Subdomain form (preferred): apiBaseUrl=https://matching.stawi.org → ""
 * - Legacy gateway form: apiBaseUrl=https://api.stawi.org → "/matching"
 *
 * Call sites pass paths relative to the matching mux root (e.g. "/me").
 */
export function matchingPathPrefix(apiBaseUrl?: string): string {
  if (!apiBaseUrl) return '/matching';
  try {
    const host = new URL(apiBaseUrl).hostname.toLowerCase();
    if (host.startsWith('matching.')) return '';
    if (host.startsWith('api.')) return '/matching';
  } catch {
    /* fall through */
  }
  // Unknown host: keep gateway prefix for safety.
  return '/matching';
}

/** Join prefix + path; strips a leading /matching if present. */
export function matchingPath(path: string, apiBaseUrl?: string): string {
  let bare = path.startsWith('/') ? path : `/${path}`;
  if (bare === '/matching' || bare.startsWith('/matching/')) {
    bare = bare.slice('/matching'.length) || '/';
  }
  // /matching/api/... legacy aliases → /api/...
  return `${matchingPathPrefix(apiBaseUrl)}${bare}`;
}
