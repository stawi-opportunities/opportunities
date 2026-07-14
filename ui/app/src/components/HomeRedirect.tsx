import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';

/**
 * Marketing homepage (`/`): any signed-in user is sent straight to
 * `/dashboard/`. Unpaid users are bounced onward by Dashboard itself
 * (→ `/onboarding/`); that keeps "/" simple and predictable.
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

    if (hero) hero.style.display = 'none';
    window.location.replace('/dashboard/');
  }, [state]);

  return null;
}
