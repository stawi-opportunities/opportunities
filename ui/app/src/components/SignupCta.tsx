import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';

export default function SignupCta() {
  const { state, login } = useAuth();

  useEffect(() => {
    const ctaMount = document.getElementById('mount-get-started-cta');
    if (!ctaMount) return;
    ctaMount.style.display = state === 'authenticated' ? 'none' : '';
  }, [state]);

  useEffect(() => {
    const section = document.getElementById('signup-cta-section');
    if (!section) return;

    if (state === "authenticated") {
      section.style.display = "none";
      return;
    }
    section.style.display = "";

    const btn = section.querySelector<HTMLAnchorElement>("[data-signup-cta]");
    if (!btn) return;

    const onClick = async (e: MouseEvent) => {
      e.preventDefault();
      const href = (btn as HTMLAnchorElement).href || "/onboarding/";
      try {
        await login();
        window.location.href = "/onboarding/";
      } catch {
        window.location.href = href;
      }
    };

    btn.addEventListener("click", onClick);
    return () => btn.removeEventListener("click", onClick);
  }, [state, login]);

  return null;
}