/**
 * Post-login destination resolution.
 *
 * After a successful sign-in (or when a signed-in user hits the marketing
 * home), the only application landings are:
 *
 *  - `/dashboard/`  — active subscription
 *  - `/onboarding/` — everyone else (preserve `?plan=` when mid-onboarding)
 *
 * Public content pages (/jobs, /search, …) are never post-login targets.
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

export type SubscriptionStatus = 'active' | 'none' | 'canceled' | 'past_due' | string;

/**
 * Decide where to send the browser after a successful OIDC code exchange
 * (or when bouncing an already-authenticated user off the marketing home).
 *
 * `returnTo` is only used to preserve onboarding query params (e.g. ?plan=pro)
 * or a dashboard section hash (e.g. #matches). It never restores public pages.
 */
export function resolvePostLoginPath(
  returnTo: string | null | undefined,
  subscriptionStatus: SubscriptionStatus
): string {
  const dest = sanitizeReturnTo(returnTo);
  const active = subscriptionStatus === 'active';

  if (active) {
    // Resume a dashboard section hash when they signed in from there.
    if (dest.startsWith('/dashboard')) return dest;
    return '/dashboard/';
  }

  // Unpaid / unknown: always onboarding. Keep plan query if they were mid-flow.
  if (dest.startsWith('/onboarding')) {
    return dest;
  }
  return '/onboarding/';
}
