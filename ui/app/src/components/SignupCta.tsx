import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';

export default function SignupCta() {
  const { hasSession, ready, login } = useAuth();

  useEffect(() => {
    const ctaMount = document.getElementById('mount-get-started-cta');
    if (!ctaMount) return;
    // Hide marketing CTAs for signed-in users (incl. token refresh).
    // While auth is restoring with a sticky session, keep them hidden too.
    ctaMount.style.display = hasSession ? 'none' : '';
  }, [hasSession]);

  useEffect(() => {
    const section = document.getElementById('signup-cta-section');
    if (!section) return;

    // Authenticated visitors see nothing -- one fewer "please sign up"
    // reminder on every page they visit while logged in.
    if (hasSession) {
      section.style.display = 'none';
      return;
    }
    // Don't flash CTA while session restore is still running for a returning user.
    if (!ready) {
      section.style.display = 'none';
      return;
    }
    section.style.display = '';

    const btn = section.querySelector<HTMLAnchorElement>('[data-signup-cta]');
    if (!btn) return;

    const onClick = async (e: MouseEvent) => {
      e.preventDefault();
      // Same path as nav Sign in — never send users to /onboarding/ first
      // (that page dead-ends when auth is down).
      try {
        await login();
      } catch {
        // Stay on page; user can retry. Dismiss / network errors are silent.
      }
    };

    btn.addEventListener('click', onClick);
    return () => btn.removeEventListener('click', onClick);
  }, [hasSession, ready, login]);

  // This island's entire job is DOM side-effects on the Hugo-rendered
  // block above; nothing new to render here.
  return null;
}
