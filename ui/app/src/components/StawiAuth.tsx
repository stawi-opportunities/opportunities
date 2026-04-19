import { useEffect, useRef, useState } from "react";
import { mount, type MountHandle } from "@stawi/profile";
import { getConfig } from "@/utils/config";
import { useAuth } from "@/providers/AuthProvider";

/**
 * Auth control in the top nav.
 *
 * Unauthenticated → renders our own "Sign in" button. The widget's
 * built-in button catches ALL errors silently
 * (`ensureAuthenticated().catch(() => {})` inside @stawi/profile),
 * which meant pop-up-blocker, OAuth-state, and token-exchange failures
 * looked identical to "button does nothing" in the browser. Our button
 * calls `login()` from the AuthProvider which surfaces failures as a
 * console.error plus an inline banner, so the user at least sees a
 * signal when something went wrong.
 *
 * Authenticated → mounts @stawi/profile which renders the avatar +
 * profile popover (the piece of the widget that isn't broken).
 */
export function StawiAuth() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<MountHandle | null>(null);
  const [busy, setBusy] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);
  const { login, state } = useAuth();

  useEffect(() => {
    // Only mount the widget when authenticated — it's useful for the
    // avatar menu + profile popover, not for the sign-in button we
    // bypass above.
    if (state !== "authenticated") {
      handleRef.current?.unmount();
      handleRef.current = null;
      return;
    }
    const host = hostRef.current;
    if (!host) return;
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
        onLogout: () => { window.location.href = "/"; },
      });
    } catch (err) {
      console.error("[auth] profile widget mount failed:", err);
    }
    return () => {
      handleRef.current?.unmount();
      handleRef.current = null;
    };
  }, [state]);

  const handleSignIn = async () => {
    setBusy(true);
    setErrorMsg(null);
    try {
      await login();
    } catch (err) {
      console.error("[auth] sign-in failed:", err);
      // Surface the specific failure mode. The auth-runtime throws
      // AuthError with a `code` field — see
      // @stawi/auth-runtime/dist/index.d.ts.
      const code = (err as { code?: string })?.code;
      if (code === "OAUTH_POPUP_BLOCKED") {
        setErrorMsg("Pop-ups are blocked. Allow pop-ups for jobs.stawi.org and try again.");
      } else if (code === "OAUTH_POPUP_CLOSED") {
        // User aborted — don't shout about it.
      } else if (err instanceof Error) {
        setErrorMsg(`Sign-in failed: ${err.message}`);
      } else {
        setErrorMsg("Sign-in failed. Please try again.");
      }
    } finally {
      setBusy(false);
    }
  };

  // Authenticated → let the widget render its avatar/popover.
  if (state === "authenticated") {
    return (
      <div className="relative" aria-label="Account" role="region">
        <div ref={hostRef} />
      </div>
    );
  }

  // Everything else (initializing / unauthenticated / refreshing / error):
  // own button with real click handler and visible failure surfacing.
  return (
    <div className="relative" aria-label="Account" role="region">
      <button
        type="button"
        onClick={() => void handleSignIn()}
        disabled={busy || state === "initializing"}
        className="rounded-md bg-navy-900 px-4 py-2 text-base font-medium text-white hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-navy-900 disabled:opacity-60"
      >
        {busy ? "Signing in…" : "Sign in"}
      </button>
      {errorMsg && (
        <p
          role="alert"
          className="absolute right-0 top-full mt-2 w-72 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 shadow-sm"
        >
          {errorMsg}
        </p>
      )}
    </div>
  );
}
