import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';
import { useSubscription } from '@/hooks/useSubscription';
import { isPaidSubscriptionStatus } from '@/hooks/useSubscriptionGate';
import { safeReplace } from '@/utils/safeNavigate';

/**
 * Marketing homepage (`/`): signed-in users leave the marketing shell.
 * - Subscribed → `/dashboard/`
 * - Unpaid after load → `/onboarding/` (paywall funnel)
 * - API error → `/dashboard/` (retry shell; never paywall)
 *
 * Uses thrash-safe navigation so we never cycle with onboarding/dashboard.
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

    // Wait for a definitive subscription answer before choosing an island.
    if (subQ.data == null && (subQ.isLoading || subQ.isFetching || subQ.isPending)) return;

    if (isPaidSubscriptionStatus(subQ.data?.status)) {
      safeReplace('/dashboard/');
      return;
    }
    if (subQ.isError && subQ.data == null) {
      safeReplace('/dashboard/');
      return;
    }
    // Definitive unpaid only.
    if (subQ.data?.status === 'none' || subQ.data?.status === 'cancelled') {
      safeReplace('/onboarding/');
    }
  }, [
    hasSession,
    ready,
    state,
    subQ.isLoading,
    subQ.isFetching,
    subQ.isPending,
    subQ.data?.status,
    subQ.isError,
  ]);

  return null;
}
