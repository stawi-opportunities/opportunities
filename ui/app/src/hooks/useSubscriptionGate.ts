import { useEffect, useState } from 'react';
import { useSubscription } from '@/hooks/useSubscription';
import { useAuth } from '@/providers/AuthProvider';

const ONBOARDING_PATH = '/onboarding/';

/** True while Flutterwave return / checkout recovery is in progress on dashboard. */
export function isBillingReturnPath(search = typeof window !== 'undefined' ? window.location.search : ''): boolean {
  try {
    const params = new URLSearchParams(search);
    const billing = params.get('billing');
    return billing === 'success' || billing === 'pending' || billing === 'failed';
  } catch {
    return false;
  }
}

function isPaidStatus(status: string | undefined | null): boolean {
  return status === 'active' || status === 'past_due' || status === 'trial';
}

/**
 * Dashboard access requires an active (or grace) subscription.
 * Unpaid candidates are sent to onboarding paywall — no free-tier dashboard stay.
 * Checkout return URLs (?billing=…) are allowed so webhooks can settle.
 */
export function useSubscriptionGate(): { checking: boolean } {
  const { hasSession, ready: authReady } = useAuth();
  const subQ = useSubscription();
  const [redirecting, setRedirecting] = useState(false);

  useEffect(() => {
    if (!authReady || !hasSession) return;
    if (redirecting) return;
    if (isBillingReturnPath()) return;
    // Wait for first successful fetch; do not kick on network error (paid users).
    if (subQ.isLoading && subQ.data == null) return;
    if (subQ.isError && subQ.data == null) return;
    if (isPaidStatus(subQ.data?.status)) return;

    setRedirecting(true);
    window.location.replace(ONBOARDING_PATH);
  }, [authReady, hasSession, redirecting, subQ.isLoading, subQ.isError, subQ.data]);

  const waitingOnSub =
    hasSession && authReady && !isBillingReturnPath() && subQ.isLoading && subQ.data == null;

  return {
    checking: Boolean(hasSession && (waitingOnSub || redirecting)),
  };
}
