/**
 * Hard navigation helpers that cannot thrash between app islands.
 *
 * A paid user with an incomplete profile used to bounce forever:
 *   /onboarding/ (paid → dashboard) ↔ /dashboard/ (incomplete → onboarding)
 *
 * Rules:
 * - Prefer a single replace per destination.
 * - If we reverse direction between onboarding and dashboard within a short
 *   window, settle on /dashboard/ (stable home for authenticated users).
 * - Never auto-bounce dashboard → onboarding → dashboard.
 */

const THRASH_MS = 4_000;

type NavMark = { path: string; at: number };

let lastNav: NavMark | null = null;

function normalizePath(path: string): string {
  try {
    const u = new URL(path, 'http://local.invalid');
    const p = u.pathname.endsWith('/') ? u.pathname : `${u.pathname}/`;
    return p + (u.search || '') + (u.hash || '');
  } catch {
    return path;
  }
}

function island(path: string): 'onboarding' | 'dashboard' | 'other' {
  const p = normalizePath(path);
  if (p.startsWith('/onboarding')) return 'onboarding';
  if (p.startsWith('/dashboard')) return 'dashboard';
  return 'other';
}

/**
 * window.location.replace with thrash protection between onboarding and dashboard.
 */
export function safeReplace(path: string): void {
  if (typeof window === 'undefined') return;

  const next = normalizePath(path);
  const now = Date.now();
  const curIsland = island(window.location.pathname);
  const nextIsland = island(next);

  // Already on that island (path prefix) — no-op for island switches.
  if (curIsland !== 'other' && curIsland === nextIsland) {
    // Same island: still allow hash/query updates via full path change.
    if (
      normalizePath(window.location.pathname + window.location.search + window.location.hash) ===
      next
    ) {
      return;
    }
  }

  if (
    lastNav &&
    now - lastNav.at < THRASH_MS &&
    ((island(lastNav.path) === 'onboarding' && nextIsland === 'dashboard') ||
      (island(lastNav.path) === 'dashboard' && nextIsland === 'onboarding') ||
      (curIsland === 'onboarding' && nextIsland === 'dashboard') ||
      (curIsland === 'dashboard' && nextIsland === 'onboarding'))
  ) {
    // Settle on dashboard — never re-enter the bounce.
    const settle = '/dashboard/';
    lastNav = { path: settle, at: now };
    if (island(window.location.pathname) === 'dashboard') {
      return;
    }
    window.location.replace(settle);
    return;
  }

  lastNav = { path: next, at: now };
  window.location.replace(next);
}

/** Test seam. */
export function __resetSafeNavigateForTests(): void {
  lastNav = null;
}
