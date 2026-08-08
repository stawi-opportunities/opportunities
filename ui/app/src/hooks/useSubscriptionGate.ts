import { useEffect, useState } from 'react';
import { useSubscription } from '@/hooks/useSubscription';
import { useAuth } from '@/providers/AuthProvider';
import { safeReplace } from '@/utils/safeNavigate';
import { isBillingReturnPath, isPaidSubscriptionStatus, resolveUserStage } from '@/utils/userStage';

export { isBillingReturnPath, isPaidSubscriptionStatus };

const ONBOARDING_PATH = '/onboarding/';

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
  /** Canonical journey stage when fully resolved from subscription alone. */
  stage?: string;
};

/**
 * Pure access decision for the dashboard shell — derived from resolveUserStage
 * so it cannot disagree with HomeRedirect / Onboarding / banners.
 */
export function evaluateSubscriptionAccess(input: {
  authReady: boolean;
  hasSession: boolean;
  billingReturn: boolean;
  status: string | null | undefined;
  loading: boolean;
  error: boolean;
}): SubscriptionAccess {
  // Auth not ready / signed out: do not block the shell (no flash of gate UI).
  if (!input.authReady || !input.hasSession) {
    return {
      allowed: false,
      block: false,
      error: false,
      shouldRedirect: false,
      confirmingPayment: false,
      stage: !input.hasSession && input.authReady ? 'anonymous' : 'loading',
    };
  }

  const info = resolveUserStage({
    authReady: input.authReady,
    hasSession: input.hasSession,
    subscriptionLoading: input.loading,
    subscriptionError: input.error,
    subscriptionStatus: input.status,
    billingReturn: input.billingReturn,
    // Profile readiness is not required for subscription island decisions.
    profileLoading: false,
    profileReady: null,
  });

  switch (info.stage) {
    case 'anonymous':
      return {
        allowed: false,
        block: false,
        error: false,
        shouldRedirect: false,
        confirmingPayment: false,
        stage: info.stage,
      };
    case 'loading':
      return {
        allowed: false,
        block: true,
        error: false,
        shouldRedirect: false,
        confirmingPayment: false,
        stage: info.stage,
      };
    case 'subscription_error':
      return {
        allowed: false,
        block: true,
        error: true,
        shouldRedirect: false,
        confirmingPayment: false,
        stage: info.stage,
      };
    case 'confirming_payment':
      return {
        allowed: false,
        block: true,
        error: false,
        shouldRedirect: false,
        confirmingPayment: true,
        stage: info.stage,
      };
    case 'dashboard_ready':
    case 'dashboard_setup':
    case 'dashboard_past_due':
      return {
        allowed: true,
        block: false,
        error: false,
        shouldRedirect: false,
        confirmingPayment: false,
        stage: info.stage,
      };
    case 'onboarding_intake':
    case 'onboarding_paywall':
      return {
        allowed: false,
        block: true,
        error: false,
        shouldRedirect: true,
        confirmingPayment: false,
        stage: info.stage,
      };
    default:
      return {
        allowed: false,
        block: true,
        error: false,
        shouldRedirect: false,
        confirmingPayment: false,
        stage: info.stage,
      };
  }
}

/**
 * Dashboard product access from canonical user stage.
 * Unpaid → onboarding once. Never cycles with profile incompleteness.
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
    loading: Boolean(subQ.data == null && (subQ.isLoading || subQ.isFetching || subQ.isPending)),
    error: Boolean(subQ.isError && subQ.data == null),
  });

  useEffect(() => {
    if (!access.shouldRedirect || redirecting) return;
    setRedirecting(true);
    safeReplace(ONBOARDING_PATH);
  }, [access.shouldRedirect, redirecting]);

  const block = access.block || redirecting;

  return {
    ...access,
    block,
    checking: block,
  };
}
