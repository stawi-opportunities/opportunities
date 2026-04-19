import { useEffect, useRef } from "react";
import { mount, type MountHandle } from "@stawi/profile";
import { getConfig } from "@/utils/config";
import { authRuntime } from "@/auth/runtime";

/**
 * Mounts @stawi/profile 1.x. The widget renders its own Sign-in
 * button (with error surfacing as of widget 1.0.1) when unauthenticated
 * and the avatar + profile popover when authenticated. We pass our
 * shared authRuntime() singleton so every island sees the same auth
 * state without the widget creating a duplicate runtime.
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
        onLogout: () => { window.location.href = "/"; },
        onError: (err) => {
          // The widget already shows a banner; this hook lets us
          // route failures into OpenObserve logs alongside the
          // browser console.
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
