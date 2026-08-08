import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';
import { useSubscription } from '@/hooks/useSubscription';
import { isPaidSubscriptionStatus } from '@/hooks/useSubscriptionGate';

/**
 * Marketing homepage (`/`): signed-in users leave the marketing shell.
 * - Subscribed → `/dashboard/`
 * - Unpaid / unknown after load → `/onboarding/` (paywall funnel)
 * Never park unpaid users on the dashboard island.
 *
 * Uses sticky `hasSession` so a token refresh never un-hides the hero and
 * never cancels a redirect mid-flight (the classic logged-in/out flicker).
 */
export default function HomeRedirect() {
  const { hasSession, ready, state } = useAuth();
  const subQ = useSubscription();

  useEffect(() => {
    const hero = document.getElementById('home-hero');

    // Still restoring session — keep hero hidden if we have a sticky hint.
    if (!ready) {
      if (hasSession && hero) hero.style.display = 'none';
      return;
    }

    if (!hasSession || state === 'unauthenticated') {
      if (hero) hero.style.display = '';
      return;
    }

    if (hero) hero.style.display = 'none';

    // Wait for subscription so we do not bounce unpaid → dashboard → onboarding.
    if (subQ.isLoading && subQ.data == null) return;
    if (isPaidSubscriptionStatus(subQ.data?.status)) {
      window.location.replace('/dashboard/');
      return;
    }
    // Network / API errors must not force a re-subscribe paywall. Send to
    // dashboard; the subscription gate shows a retry shell instead of checkout.
    if (subQ.isError && subQ.data == null) {
      window.location.replace('/dashboard/');
      return;
    }
    // Confirmed unpaid → onboarding paywall funnel.
    window.location.replace('/onboarding/');
  }, [hasSession, ready, state, subQ.isLoading, subQ.data?.status, subQ.isError]);

  return null;
}
