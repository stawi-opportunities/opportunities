import { useEffect, useState } from 'react';
import { useSubscription } from '@/hooks/useSubscription';
import { useAuth } from '@/providers/AuthProvider';

const ONBOARDING_PATH = '/onboarding/';

/** True while Flutterwave return / checkout recovery is in progress on dashboard. */
export function isBillingReturnPath(
  search = typeof window !== 'undefined' ? window.location.search : ''
): boolean {
  try {
    const params = new URLSearchParams(search);
    const billing = params.get('billing');
    return billing === 'success' || billing === 'pending' || billing === 'failed';
  } catch {
    return false;
  }
}

/** Active, grace (past_due), or trial — may use the dashboard product surface. */
export function isPaidSubscriptionStatus(status: string | undefined | null): boolean {
  return status === 'active' || status === 'past_due' || status === 'trial';
}

export type SubscriptionAccess = {
  /** May render dashboard product UI (paid, or billing return while webhook settles). */
  allowed: boolean;
  /** Must not paint dashboard content (loading, unpaid redirect, or verify error). */
  block: boolean;
  /** Subscription fetch failed with no cached status. */
  error: boolean;
  /** Unpaid status known — redirect to onboarding paywall. */
  shouldRedirect: boolean;
};

/**
 * Pure access decision for the dashboard shell.
 * Dashboard content must only render when `allowed` is true.
 */
export function evaluateSubscriptionAccess(input: {
  authReady: boolean;
  hasSession: boolean;
  billingReturn: boolean;
  status: string | null | undefined;
  /** True while first subscription fetch is in flight with no data yet. */
  loading: boolean;
  /** True when the subscription query failed and we have no status. */
  error: boolean;
}): SubscriptionAccess {
  const { authReady, hasSession, billingReturn, status, loading, error } = input;

  if (!authReady || !hasSession) {
    return { allowed: false, block: false, error: false, shouldRedirect: false };
  }

  if (billingReturn) {
    return { allowed: true, block: false, error: false, shouldRedirect: false };
  }

  if (loading) {
    return { allowed: false, block: true, error: false, shouldRedirect: false };
  }

  if (error && (status == null || status === '')) {
    return { allowed: false, block: true, error: true, shouldRedirect: false };
  }

  if (isPaidSubscriptionStatus(status)) {
    return { allowed: true, block: false, error: false, shouldRedirect: false };
  }

  // Known unpaid (none / canceled / empty) — never load dashboard product UI.
  return { allowed: false, block: true, error: false, shouldRedirect: true };
}

/**
 * Dashboard access requires an active (or grace) subscription.
 * Unpaid candidates are sent to onboarding paywall — no free-tier dashboard stay.
 * Checkout return URLs (?billing=…) are allowed so webhooks can settle.
 *
 * `allowed` is synchronous with query data (no useEffect flash of dashboard UI).
 */
export function useSubscriptionGate(): SubscriptionAccess & { checking: boolean } {
  const { hasSession, ready: authReady } = useAuth();
  const subQ = useSubscription();
  const [redirecting, setRedirecting] = useState(false);

  const access = evaluateSubscriptionAccess({
    authReady,
    hasSession,
    billingReturn: isBillingReturnPath(),
    status: subQ.data?.status,
    loading: Boolean(subQ.isLoading && subQ.data == null),
    error: Boolean(subQ.isError && subQ.data == null),
  });

  useEffect(() => {
    if (!access.shouldRedirect || redirecting) return;
    setRedirecting(true);
    window.location.replace(ONBOARDING_PATH);
  }, [access.shouldRedirect, redirecting]);

  const block = access.block || redirecting;

  return {
    ...access,
    block,
    // Alias used by older call sites / tests of “still determining or leaving”.
    checking: block,
  };
}
