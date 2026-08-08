import { useEffect, useState } from 'react';
import { useSubscription } from '@/hooks/useSubscription';
import { useAuth } from '@/providers/AuthProvider';

const ONBOARDING_PATH = '/onboarding/';

/**
 * Checkout return / recovery on the dashboard URL.
 * These visits must NOT unlock product UI until /me/subscription is active.
 */
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

/**
 * True when matching billing entitlement is confirmed (product access).
 * Source of truth: GET /me/subscription.
 * Backend maps paid/trial/(legacy active) → "active"; past_due stays
 * "past_due" but remains entitled during dunning. Unpaid is "none" / "cancelled".
 */
export function isPaidSubscriptionStatus(status: string | undefined | null): boolean {
  return status === 'active' || status === 'past_due';
}

export type SubscriptionAccess = {
  /** Product dashboard UI (matches, CV, settings) may render. */
  allowed: boolean;
  /** Must not paint product UI. */
  block: boolean;
  /** Subscription fetch failed with no cached status. */
  error: boolean;
  /** Unpaid and not in payment-confirm flow — send to onboarding. */
  shouldRedirect: boolean;
  /**
   * Returned from checkout (?billing=…) but /me/subscription is not active yet.
   * Stay on a confirmation shell; do not open the product dashboard.
   */
  confirmingPayment: boolean;
};

/**
 * Pure access decision for the dashboard shell.
 *
 * Product UI is allowed only when billing entitlement is active.
 * Checkout return URLs only open a confirmation shell until that is true.
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
    return {
      allowed: false,
      block: false,
      error: false,
      shouldRedirect: false,
      confirmingPayment: false,
    };
  }

  if (loading) {
    return {
      allowed: false,
      block: true,
      error: false,
      shouldRedirect: false,
      confirmingPayment: false,
    };
  }

  if (error && (status == null || status === '')) {
    return {
      allowed: false,
      block: true,
      error: true,
      shouldRedirect: false,
      confirmingPayment: false,
    };
  }

  // Billing entitlement confirmed — only path into product dashboard.
  if (isPaidSubscriptionStatus(status)) {
    return {
      allowed: true,
      block: false,
      error: false,
      shouldRedirect: false,
      confirmingPayment: false,
    };
  }

  // Checkout return: wait for activation; never paint product UI unpaid.
  if (billingReturn) {
    return {
      allowed: false,
      block: true,
      error: false,
      shouldRedirect: false,
      confirmingPayment: true,
    };
  }

  // Known unpaid — leave dashboard entirely.
  return {
    allowed: false,
    block: true,
    error: false,
    shouldRedirect: true,
    confirmingPayment: false,
  };
}

/**
 * Dashboard product access requires GET /me/subscription status === "active"
 * (matching entitlement after billing/checkout activation).
 *
 * Unpaid users → onboarding. Checkout return → confirmation shell only.
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
    checking: block,
  };
}
