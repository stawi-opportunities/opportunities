import { useEffect, useRef } from "react";
import { mount, type MountHandle } from "@stawi/profile";
import { getConfig } from "@/utils/config";

/**
 * Mounts the @stawi/profile widget into a DOM node the moment the component
 * mounts. The widget is the entire auth UI surface:
 *   unauthenticated → "Login" button
 *   initializing    → loading pulse
 *   authenticated   → avatar badge + dropdown (profile, admin link, logout)
 *
 * The widget uses its own shadow DOM, so its styles are isolated from our
 * Tailwind. That is intentional — every Stawi frontend gets a visually
 * identical auth control by dropping in the same mount.
 */
export function StawiAuth() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<MountHandle | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    const cfg = getConfig();
    handleRef.current = mount({
      target: host,
      clientId: cfg.oidcClientID,
      installationId: cfg.oidcInstallationID,
      idpBaseUrl: cfg.oidcIssuer,
      apiBaseUrl: cfg.candidatesAPIURL,
      theme: "light",
      onLogout: () => {
        window.location.href = "/";
      },
    });

    return () => {
      handleRef.current?.unmount();
      handleRef.current = null;
    };
  }, []);

  return <div ref={hostRef} aria-label="Account" />;
}
