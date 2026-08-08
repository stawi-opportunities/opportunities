/**
 * Post-login destination resolution.
 *
 * Content return paths win. Otherwise destination is the canonical
 * resolveUserStage homePath so auth callback cannot disagree with
 * HomeRedirect / Onboarding leave / subscription gate for the same status:
 *
 *   entitled (active | past_due)     → /dashboard/
 *   unpaid (none | cancelled|…)      → /onboarding/
 *   unknown (trial, weird, …)        → /dashboard/  (subscription_error shell)
 *
 * Profile readiness is not loaded at callback (profileReady=null), so
 * unpaid defaults to intake and entitled defaults to dashboard_setup —
 * both stable homes that match later full context resolution.
 */

import { resolveUserStage } from '@/utils/userStage';

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
 * Otherwise uses resolveUserStage.homePath (same island as every other surface).
 */
export function resolvePostLoginPath(
  returnTo: string | null | undefined,
  subscriptionStatus: SubscriptionStatus
): string {
  const dest = sanitizeReturnTo(returnTo);

  // Login-to-apply / mid-browse: always restore the listing.
  if (isContentReturnPath(dest)) {
    return dest;
  }

  const stage = resolveUserStage({
    authReady: true,
    hasSession: true,
    subscriptionLoading: false,
    subscriptionError: false,
    subscriptionStatus,
    billingReturn: false,
    // Profile not loaded at callback — unpaid → intake; entitled → setup home.
    profileLoading: false,
    profileReady: null,
  });

  // Preserve deep links only when already on the stage island.
  if (stage.onboardingAllowed) {
    if (dest.startsWith('/onboarding')) return dest;
    return stage.homePath;
  }

  if (dest.startsWith('/dashboard')) return dest;
  return stage.homePath;
}
