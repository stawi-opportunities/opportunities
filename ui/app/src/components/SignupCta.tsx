import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';

export default function SignupCta() {
  const { state } = useAuth();

  useEffect(() => {
    const ctaMount = document.getElementById('mount-get-started-cta');
    if (!ctaMount) return;
    ctaMount.style.display = state === 'authenticated' ? 'none' : '';
  }, [state]);

    // Authenticated visitors see nothing — one fewer "please sign up"
    // reminder on every page they visit while logged in.
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
        // Auth widget not configured or user dismissed — fall back to
        // direct navigation so the button never silently does nothing.
        window.location.href = href;
      }
    };

    btn.addEventListener("click", onClick);
    return () => btn.removeEventListener("click", onClick);
  }, [state, login]);

  // This island's entire job is DOM side-effects on the Hugo-rendered
  // block above; nothing new to render here.
  return null;
}
