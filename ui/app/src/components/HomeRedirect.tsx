import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';

/**
 * Marketing homepage (`/`): signed-in users go to `/dashboard/`.
 * Unpaid users are bounced onward by Dashboard itself (→ `/onboarding/`).
 *
 * Uses sticky `hasSession` so a token refresh never un-hides the hero and
 * never cancels a redirect mid-flight (the classic logged-in/out flicker).
 */
export default function HomeRedirect() {
  const { hasSession, ready, state } = useAuth();

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
    window.location.replace('/dashboard/');
  }, [hasSession, ready, state]);

  return null;
}
