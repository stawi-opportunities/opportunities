/**
 * Post-login destination resolution.
 *
 * @stawi/auth-runtime stashes the path the user was on when they clicked
 * Sign in (`returnTo`) and returns it from `completeRedirect()`. We combine
 * that with subscription status so:
 *
 *  - paid users resume where they left off (or land on the dashboard)
 *  - unpaid users finish onboarding unless they were on a public content page
 *  - auth/callback itself is never a final landing
 */

/** Paths that do not require an active subscription to view. */
const PUBLIC_PREFIXES = [
  '/jobs',
  '/job/',
  '/search',
  '/scholarships',
  '/scholarship/',
  '/tenders',
  '/tender/',
  '/deals',
  '/deal/',
  '/funding',
  '/funding-detail',
  '/categories',
  '/l/',
  '/about',
  '/pricing',
  '/faq',
  '/terms',
  '/privacy',
] as const;

/**
 * Sanitize a stashed return path. Only same-origin relative paths are
 * allowed — rejects protocol-relative (`//evil.com`) and absolute URLs.
 */
export function sanitizeReturnTo(raw: string | null | undefined): string {
  if (!raw || typeof raw !== 'string') return '/';
  const trimmed = raw.trim();
  if (!trimmed.startsWith('/') || trimmed.startsWith('//')) return '/';
  // Keep search (e.g. /onboarding/?plan=pro) and hash (e.g. /dashboard/#matches).
  try {
    const u = new URL(trimmed, 'http://local.invalid');
    return (u.pathname || '/') + (u.search || '') + (u.hash || '');
  } catch {
    return '/';
  }
}

export function isPublicContentPath(path: string): boolean {
  const p = sanitizeReturnTo(path);
  if (p === '/') return false; // marketing home is not "resume here" for unpaid
  return PUBLIC_PREFIXES.some((prefix) => p === prefix || p.startsWith(prefix));
}

export type SubscriptionStatus = 'active' | 'none' | 'canceled' | 'past_due' | string;

/**
 * Decide where to send the browser after a successful OIDC code exchange.
 */
export function resolvePostLoginPath(
  returnTo: string | null | undefined,
  subscriptionStatus: SubscriptionStatus
): string {
  const dest = sanitizeReturnTo(returnTo);
  const active = subscriptionStatus === 'active';

  // Never land on the callback (or any /auth/*) page.
  if (dest.startsWith('/auth/') || dest.startsWith('/auth?')) {
    return active ? '/dashboard/' : '/onboarding/';
  }

  if (active) {
    // Paid: resume deep link; treat home / onboarding as "go to dashboard".
    if (dest === '/' || dest.startsWith('/onboarding')) {
      return '/dashboard/';
    }
    return dest;
  }

  // Unpaid / unknown: finish onboarding unless they were browsing public content.
  if (dest.startsWith('/onboarding')) {
    return dest; // preserve ?plan=
  }
  if (isPublicContentPath(dest)) {
    return dest;
  }
  // Home, dashboard, settings, unknown gated surfaces → onboarding.
  return '/onboarding/';
}
