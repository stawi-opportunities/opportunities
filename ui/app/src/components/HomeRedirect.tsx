import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';

export default function HomeRedirect() {
  const { state } = useAuth();

  useEffect(() => {
    const hero = document.getElementById('home-hero');
    if (state === 'authenticated') {
      // Keep the hero hidden (the inline script may already have done so)
      // and move the user to their dashboard.
      if (hero) hero.style.display = 'none';
      window.location.replace('/dashboard/');
    } else if (state === 'unauthenticated') {
      // Reveal the hero — covers the case where the synchronous hint was
      // stale (e.g. signed out in another tab) and hid it pre-emptively.
      if (hero) hero.style.display = '';
    }
  }, [state]);

  return null;
}
