import { useEffect, useState } from 'react';
import { AuthError } from '@stawi/auth-runtime';
import { authRuntime } from '@/auth/runtime';
import { resolvePostLoginPath } from '@/auth/postLoginRedirect';
import { fetchMeSubscription } from '@/api/candidates';
import { useAuth } from '@/providers/AuthProvider';

/**
 * /auth/callback/ — landing page for the OIDC full-page redirect.
 *
 * Flow:
 *  1. `completeRedirect()` exchanges ?code=&state= and returns the path the
 *     user was on when they clicked Sign in (`returnTo`).
 *  2. We load subscription status once and pick a final destination via
 *     `resolvePostLoginPath` (paid → resume/dashboard; unpaid → onboarding
 *     unless they were on a public content page).
 *  3. `location.replace` (not assign) so Back does not re-run the exchange.
 */
export default function AuthCallback() {
  const { login } = useAuth();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const rt = authRuntime();

    // Already signed in and landed here without a code (e.g. Back button,
    // bookmark) — skip the exchange and route immediately.
    const params = new URLSearchParams(window.location.search);
    const hasCode = params.has('code') && params.has('state');

    const finish = async () => {
      let returnTo = '/';
      if (hasCode) {
        const result = await rt.completeRedirect();
        returnTo = result.returnTo || '/';
      } else if (rt.getState() === 'authenticated') {
        returnTo = '/';
      } else {
        throw new AuthError('OAUTH_FAILED', 'missing code/state on callback URL');
      }
      if (cancelled) return;

      // Single gate: payment status + stashed returnTo.
      // fetchMeSubscription falls back to {status:"none"} on any failure so a
      // wedged matching service degrades to onboarding (safer for unknown).
      const sub = await fetchMeSubscription();
      if (cancelled) return;
      const target = resolvePostLoginPath(returnTo, sub.status);
      window.location.replace(target);
    };

    finish().catch((err: unknown) => {
      if (cancelled) return;
      const code = err instanceof AuthError ? err.code : 'unknown';
      const message =
        code === 'OAUTH_REDIRECT_STORAGE_MISSING'
          ? 'Your sign-in session expired before it could complete. Please sign in again.'
          : code === 'OAUTH_STATE_MISMATCH'
            ? "The sign-in response didn't match our request. Please sign in again."
            : "We couldn't complete sign-in. Please try again.";
      setError(message);
    });

    return () => {
      cancelled = true;
    };
  }, []);

  if (error) {
    return (
      <div className="flex min-h-[60vh] items-center justify-center px-4 py-16">
        <div className="w-full max-w-md text-center">
          <h1 className="text-xl font-semibold text-gray-900">Sign-in didn&apos;t complete</h1>
          <p className="mt-3 text-sm text-gray-600">{error}</p>
          <div className="mt-6 flex flex-col items-center gap-3 sm:flex-row sm:justify-center">
            <button
              type="button"
              onClick={() => {
                void login();
              }}
              className="inline-flex items-center rounded-md bg-navy-900 px-5 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
            >
              Sign in again
            </button>
            <a
              href="/"
              className="inline-flex items-center rounded-md border border-gray-300 bg-white px-5 py-2.5 text-sm font-medium text-gray-700 shadow-sm hover:bg-gray-50"
            >
              Back to home
            </a>
          </div>
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
