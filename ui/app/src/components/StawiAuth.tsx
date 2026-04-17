import { useEffect, useRef, useState } from "react";
import { mount, type MountHandle } from "@stawi/profile";
import { getConfig } from "@/utils/config";
import { useAuth } from "@/providers/AuthProvider";

/**
 * Mounts the @stawi/profile widget: "Sign in" button when logged out, avatar
 * badge when logged in. The widget owns its own shadow DOM so every Stawi
 * frontend gets a visually identical auth control.
 *
 * Fallback: if the widget script fails to load (ad-blocker, offline) we
 * render a plain sign-in button after 2.5s so the user is never stranded
 * without an auth affordance.
 */
export function StawiAuth() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<MountHandle | null>(null);
  const [widgetFailed, setWidgetFailed] = useState(false);
  const { login, state } = useAuth();

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    const cfg = getConfig();
    let mounted = false;
    try {
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
      mounted = true;
    } catch {
      setWidgetFailed(true);
    }

    // If the widget hasn't painted anything interactive after 2.5s, assume
    // it's broken and reveal the fallback button.
    const timer = window.setTimeout(() => {
      if (host.childElementCount === 0) setWidgetFailed(true);
    }, 2500);

    return () => {
      window.clearTimeout(timer);
      if (mounted) {
        handleRef.current?.unmount();
        handleRef.current = null;
      }
    };
  }, []);

  return (
    <div className="relative" aria-label="Account" role="region">
      <div ref={hostRef} />
      {widgetFailed && state !== "authenticated" && (
        <button
          type="button"
          onClick={() => void login()}
          className="rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500"
        >
          Sign in
        </button>
      )}
    </div>
  );
}
