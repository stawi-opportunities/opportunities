import { useEffect, useRef } from "react";
import { mount, type MountHandle } from "@stawi/profile";
import { getConfig } from "@/utils/config";
import { authRuntime } from "@/auth/runtime";

// Token + CSS overrides so the widget's sign-in button inherits the
// stawi.jobs design system (navy-900 primary, rounded-md, Tailwind's
// 'text-base' font-size and font-semibold weight) rather than the
// widget's default claudeLight palette.
//
// Hex values mirror tailwind.config.js navy-{800,900} exactly; keeping
// them as literals here (instead of CSS variables) avoids a shadow-DOM
// leak — the widget's shadow root can't see the host page's CSS
// variables.
const STAWI_JOBS_TOKENS = {
  colorPrimary: "#0c1226",       // tw navy-900
  colorPrimaryHover: "#141a33",  // tw navy-800
  colorFocusRing: "#0c1226",
  radius: "6px",                 // tw rounded-md; matches /pricing/ cards etc.
  fontHeading: `"Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`,
  fontBody: `"Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`,
  fontWeightHeading: 600,
} as const;

// Size + shape overrides the token API doesn't cover. Matches the
// .rounded-md.bg-navy-900.px-5.py-2.5.text-sm.font-semibold pattern
// used elsewhere (pricing CTA, nav sign-in fallback, 404 CTA).
const STAWI_JOBS_BUTTON_CSS = `
  .aiw-signin-trigger {
    padding: 10px 20px;
    font-size: 14px;
    line-height: 1.25rem;
    letter-spacing: 0;
    border-radius: 6px;
    gap: 6px;
    box-shadow: 0 1px 2px rgba(12, 18, 38, 0.08);
  }
  .aiw-signin-trigger:hover {
    box-shadow: 0 2px 6px rgba(12, 18, 38, 0.18);
  }
  .aiw-signin-avatar {
    width: 16px;
    height: 16px;
  }
`;

/**
 * Mounts @stawi/profile 1.x with stawi.jobs' visual tokens.
 * Unauthenticated → renders the pill/navy Sign-in button; authed →
 * avatar + profile popover. We pass our singleton auth runtime so
 * the widget's token store and our API client stay in sync.
 */
export function StawiAuth() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<MountHandle | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    if ((host.shadowRoot || host.childElementCount > 0) && handleRef.current) return;

    const cfg = getConfig();
    try {
      handleRef.current = mount({
        target: host,
        runtime: authRuntime(),
        installationId: cfg.oidcInstallationID,
        clientId: cfg.oidcClientID,
        idpBaseUrl: cfg.oidcIssuer,
        apiBaseUrl: cfg.candidatesAPIURL,
        theme: "light",
        tokens: STAWI_JOBS_TOKENS,
        css: STAWI_JOBS_BUTTON_CSS,
        onLogout: () => { window.location.href = "/"; },
        onError: (err) => {
          console.error("[stawi/profile] onError:", err);
        },
      });
    } catch (err) {
      console.error("[stawi/profile] mount failed:", err);
    }

    return () => {
      handleRef.current?.unmount();
      handleRef.current = null;
    };
  }, []);

  return (
    <div className="relative" aria-label="Account" role="region">
      <div ref={hostRef} />
    </div>
  );
}
