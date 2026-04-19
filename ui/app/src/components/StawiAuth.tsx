import { useEffect, useRef } from "react";
import { mount, type MountHandle } from "@stawi/profile";
import { getConfig } from "@/utils/config";
import { authRuntime } from "@/auth/runtime";
import { profileWidgetTokens, profileWidgetCSS } from "@/theme/profile-widget";

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
        tokens: profileWidgetTokens,
        css: profileWidgetCSS,
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
