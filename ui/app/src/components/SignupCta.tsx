import { useEffect } from "react";
import { useAuth } from "@/providers/AuthProvider";

/**
 * Island for the "Two minutes to set up" block Hugo renders above
 * the footer on every page. Two jobs:
 *
 *   1. Hide the block for authenticated users — they've already
 *      signed up, the CTA is noise at that point.
 *   2. Intercept the Get-started button so clicking it triggers the
 *      widget's login flow instead of a bare redirect. On successful
 *      auth we forward to /onboarding/, matching the user journey
 *      the product team mapped out.
 *
 * The fallback <a href="/onboarding/"> on the button stays — with JS
 * disabled we let the browser navigate directly; /onboarding/ itself
 * will prompt for sign-in via the widget if needed.
 */
export default function SignupCta() {
  const { state, login } = useAuth();

  useEffect(() => {
    // #signup-cta-section is the visible block Hugo rendered; this
    // island's host is a separate empty <div#mount-signup-cta>, so
    // toggling display on the section doesn't fight React.
    const section = document.getElementById("signup-cta-section");
    if (!section) return;

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
