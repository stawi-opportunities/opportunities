import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';
import { fetchMeSubscription } from '@/api/candidates';
import { resolvePostLoginPath } from '@/auth/postLoginRedirect';

/**
 * On the marketing homepage: if the visitor is already signed in, send them
 * to the right app surface in one hop (dashboard if paid, onboarding if not).
 * Avoids home → dashboard → onboarding double redirects for unpaid users.
 */
export default function HomeRedirect() {
  const { state } = useAuth();

  useEffect(() => {
    const hero = document.getElementById('home-hero');

    if (state === 'unauthenticated') {
      if (hero) hero.style.display = '';
      return;
    }

    if (state !== 'authenticated') return;

    // Keep the hero hidden while we decide where to send them.
    if (hero) hero.style.display = 'none';

    let cancelled = false;
    (async () => {
      const sub = await fetchMeSubscription();
      if (cancelled) return;
      // returnTo = "/" so resolvePostLoginPath applies home→dashboard/onboarding.
      const target = resolvePostLoginPath('/', sub.status);
      window.location.replace(target);
    })().catch(() => {
      if (!cancelled) window.location.replace('/onboarding/');
    });

    return () => {
      cancelled = true;
    };
  }, [state]);

  return null;
}
