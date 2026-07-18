/**
 * Post-login destination resolution.
 *
 * After a successful sign-in:
 *  - Opportunity detail pages are restored when the user signed in to apply
 *    (browse free, login-to-apply).
 *  - `/dashboard/`  — active subscription (default app surface)
 *  - `/onboarding/` — unpaid default when no content return path
 */

/**
 * Sanitize a stashed return path. Only same-origin relative paths are
 * allowed — rejects protocol-relative (`//evil.com`) and absolute URLs.
 */
export function sanitizeReturnTo(raw: string | null | undefined): string {
  if (!raw || typeof raw !== 'string') return '/';
  const trimmed = raw.trim();
  if (!trimmed.startsWith('/') || trimmed.startsWith('//')) return '/';
  try {
    const u = new URL(trimmed, 'http://local.invalid');
    return (u.pathname || '/') + (u.search || '') + (u.hash || '');
  } catch {
    return '/';
  }
}

/** Public listing detail paths users may return to after login-to-apply. */
const CONTENT_DETAIL = /^\/(jobs|scholarships|tenders|deals|funding)\/[^/]+\/?/;

/**
 * True when returnTo is an opportunity detail page (or has apply intent).
 * Browse is free; apply requires auth — we must restore the listing after login.
 */
export function isContentReturnPath(path: string): boolean {
  const dest = sanitizeReturnTo(path);
  if (CONTENT_DETAIL.test(dest.split('?')[0] ?? '')) return true;
  try {
    const u = new URL(dest, 'http://local.invalid');
    if (u.searchParams.get('apply') === '1') return true;
  } catch {
    /* ignore */
  }
  return false;
}

export type SubscriptionStatus = 'active' | 'none' | 'canceled' | 'past_due' | string;

/**
 * Decide where to send the browser after a successful OIDC code exchange.
 *
 * Content return paths (job detail after "Sign in to apply") win for everyone.
 * Otherwise active → dashboard; unpaid → onboarding (preserve plan query).
 */
export function resolvePostLoginPath(
  returnTo: string | null | undefined,
  subscriptionStatus: SubscriptionStatus
): string {
  const dest = sanitizeReturnTo(returnTo);
  const active =
    subscriptionStatus === 'active' ||
    subscriptionStatus === 'past_due' ||
    subscriptionStatus === 'trial';

  // Login-to-apply / mid-browse: always restore the listing.
  if (isContentReturnPath(dest)) {
    return dest;
  }

  if (active) {
    if (dest.startsWith('/dashboard')) return dest;
    return '/dashboard/';
  }

  if (dest.startsWith('/onboarding')) {
    return dest;
  }
  return '/onboarding/';
}
