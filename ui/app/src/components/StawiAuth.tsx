import { useEffect, useRef, useState } from "react";
import { mount, type MountHandle } from "@stawi/profile";
import { getConfig } from "@/utils/config";
import { useAuth } from "@/providers/AuthProvider";

/**
 * Mounts the @stawi/profile widget — "Sign in" when logged out, avatar
 * badge when logged in. The widget owns its own shadow DOM so every
 * Stawi frontend gets a visually identical auth control.
 *
 * Fallback: if the widget package fails to load (ad-blocker, offline,
 * 404 on the bundle) we render a plain sign-in button after 3 s so the
 * user is never stranded without an auth affordance. We detect a
 * successful mount by looking for either `host.shadowRoot` or any
 * attached child — the widget may paint into light DOM in some builds,
 * so relying on shadow root alone produced a duplicate "Sign in"
 * button next to the widget's own "Login" affordance.
 */
export function StawiAuth() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<MountHandle | null>(null);
  const [widgetFailed, setWidgetFailed] = useState(false);
  const { login, state } = useAuth();

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    // React StrictMode mounts effects twice in dev; if the widget is
    // already attached (shadow root OR light-DOM children), skip the
    // redundant mount.
    if ((host.shadowRoot || host.childElementCount > 0) && handleRef.current) return;

    const cfg = getConfig();
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
    } catch {
      setWidgetFailed(true);
      return;
    }

    // Treat a missing mount signature after 3 s as a hard failure —
    // the widget's bundle never loaded or its mount threw
    // asynchronously. Either a shadow root or any rendered child
    // counts as success.
    const timer = window.setTimeout(() => {
      if (!host.shadowRoot && host.childElementCount === 0) {
        setWidgetFailed(true);
      }
    }, 3000);

    return () => {
      window.clearTimeout(timer);
      handleRef.current?.unmount();
      handleRef.current = null;
    };
  }, []);

  return (
    <div className="relative" aria-label="Account" role="region">
      <div ref={hostRef} />
      {widgetFailed && state !== "authenticated" && (
        <button
          type="button"
          onClick={() => void login()}
          className="rounded-md bg-navy-900 px-4 py-2 text-base font-medium text-white hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-navy-900"
        >
          Sign in
        </button>
      )}
    </div>
  );
}
