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
      const href = (btn as HTMLAnchorElement).href || '/onboarding/';
      try {
        await login();
        window.location.href = '/onboarding/';
      } catch {
        // Auth widget not configured or user dismissed — fall back to
        // direct navigation so the button never silently does nothing.
        window.location.href = href;
      }
    };

    btn.addEventListener('click', onClick);
    return () => btn.removeEventListener('click', onClick);
  }, [hasSession, ready, login]);

  // This island's entire job is DOM side-effects on the Hugo-rendered
  // block above; nothing new to render here.
  return null;
}
