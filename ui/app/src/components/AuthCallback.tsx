import { useEffect, useState } from "react";
import { AuthError } from "@stawi/auth-runtime";
import { authRuntime } from "@/auth/runtime";

/**
 * /auth/callback/ — landing page for the OIDC full-page redirect.
 *
 * @stawi/auth-runtime 1.1+ uses a full-page redirect rather than a
 * popup, so this route is now the real exchange page (not a popup
 * handoff). On mount we call runtime.completeRedirect(), which reads
 * `?code=` + `?state=` from the URL, exchanges the code for tokens
 * via the worker, and resolves. We then route the user to
 * /dashboard/ unconditionally — the dashboard handles the
 * "complete payment" state for unpaid candidates and renders the
 * tier-specific surface for paid ones.
 */
export default function AuthCallback() {
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const rt = authRuntime();
    rt.completeRedirect()
      .then(() => {
        if (cancelled) return;
        window.location.assign("/dashboard/");
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        const code = err instanceof AuthError ? err.code : "unknown";
        const message =
          code === "OAUTH_REDIRECT_STORAGE_MISSING"
            ? "Your sign-in session expired before it could complete. Please sign in again."
            : code === "OAUTH_STATE_MISMATCH"
              ? "The sign-in response didn't match our request. Please sign in again."
              : "We couldn't complete sign-in. Please try again.";
        setError(message);
      });
    return () => { cancelled = true; };
  }, []);

  if (error) {
    return (
      <div className="flex min-h-[60vh] items-center justify-center px-4 py-16">
        <div className="w-full max-w-md text-center">
          <h1 className="text-xl font-semibold text-gray-900">Sign-in didn't complete</h1>
          <p className="mt-3 text-sm text-gray-600">{error}</p>
          <a
            href="/"
            className="mt-6 inline-flex items-center rounded-md bg-navy-900 px-5 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
          >
            Back to home
          </a>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-[60vh] items-center justify-center px-4 py-16">
      <div className="w-full max-w-md text-center">
        <div
          className="mx-auto h-8 w-8 animate-spin rounded-full border-4 border-accent-500 border-t-transparent"
          aria-label="Completing sign in"
        />
        <p className="mt-4 text-gray-600">Completing sign in…</p>
      </div>
    </div>
  );
}
