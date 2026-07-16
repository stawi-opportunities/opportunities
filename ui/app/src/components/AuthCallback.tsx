import { useEffect, useState } from 'react';
import { AuthError, type AuthRuntime } from '@stawi/auth-runtime';
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
 *  2. We load subscription status once and route via `resolvePostLoginPath`
 *     (active → /dashboard/; otherwise → /onboarding/).
 *  3. `location.replace` so Back does not re-run the exchange.
 *
 * Strict Mode / remount safety: completion is a module-level singleton so the
 * PKCE stash is only consumed once. Cleanup never aborts navigation after a
 * successful exchange (that was showing "session expired" while the avatar
 * already showed the user as signed in).
 */

type CompletionResult = { ok: true } | { ok: false; error: string };

/** One in-flight completion per full page load (survives React remounts). */
let completionPromise: Promise<CompletionResult> | null = null;

function waitUntilAuthed(rt: AuthRuntime, timeoutMs = 2500): Promise<boolean> {
  if (rt.getState() === 'authenticated') return Promise.resolve(true);
  return new Promise((resolve) => {
    const timer = window.setTimeout(() => {
      unsub();
      resolve(rt.getState() === 'authenticated');
    }, timeoutMs);
    const unsub = rt.onAuthStateChange((s) => {
      if (s === 'authenticated') {
        window.clearTimeout(timer);
        unsub();
        resolve(true);
      }
    });
  });
}

function errorMessage(err: unknown): string {
  const code = err instanceof AuthError ? err.code : 'unknown';
  if (code === 'OAUTH_REDIRECT_STORAGE_MISSING') {
    return 'Your sign-in session expired before it could complete. Please sign in again.';
  }
  if (code === 'OAUTH_STATE_MISMATCH') {
    return "The sign-in response didn't match our request. Please sign in again.";
  }
  return "We couldn't complete sign-in. Please try again.";
}

async function runCompletion(): Promise<CompletionResult> {
  const rt = authRuntime();
  const params = new URLSearchParams(window.location.search);
  const hasCode = params.has('code') && params.has('state');

  let returnTo = '/';

  try {
    if (hasCode) {
      try {
        const result = await rt.completeRedirect();
        returnTo = result.returnTo || '/';
      } catch (err) {
        // Stash already consumed (Strict Mode double-mount, refresh) but the
        // first attempt may have stored tokens. Prefer entering the app.
        if (rt.getState() !== 'authenticated') {
          const recovered = await waitUntilAuthed(rt, 1200);
          if (!recovered) throw err;
        }
        returnTo = '/';
      }
    } else {
      // Bookmark / Back to callback without a code — only proceed if already authed.
      if (rt.getState() !== 'authenticated') {
        const authed = await waitUntilAuthed(rt, 1200);
        if (!authed) {
          throw new AuthError('OAUTH_FAILED', 'missing code/state on callback URL');
        }
      }
    }

    const sub = await fetchMeSubscription();
    const target = resolvePostLoginPath(returnTo, sub.status);
    window.location.replace(target);
    return { ok: true };
  } catch (err) {
    // Last chance: already authenticated → still leave the callback page.
    if (rt.getState() === 'authenticated') {
      try {
        const sub = await fetchMeSubscription();
        window.location.replace(resolvePostLoginPath('/', sub.status));
        return { ok: true };
      } catch {
        window.location.replace('/onboarding/');
        return { ok: true };
      }
    }
    return { ok: false, error: errorMessage(err) };
  }
}

function ensureCompletion(): Promise<CompletionResult> {
  if (!completionPromise) {
    completionPromise = runCompletion();
  }
  return completionPromise;
}

/** Test seam — reset singleton between unit tests. */
export function __resetAuthCallbackCompletionForTests(): void {
  completionPromise = null;
}

export default function AuthCallback() {
  const { login, state, hasSession } = useAuth();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    ensureCompletion().then((result) => {
      // Only update React state if still mounted; never cancel the underlying
      // completion / navigation (that caused the "logged in but error UI" bug).
      if (!alive) return;
      if (!result.ok) setError(result.error);
    });
    return () => {
      alive = false;
    };
  }, []);

  const continueToApp = () => {
    void (async () => {
      try {
        const sub = await fetchMeSubscription();
        window.location.replace(resolvePostLoginPath('/', sub.status));
      } catch {
        window.location.replace('/onboarding/');
      }
    })();
  };

  if (error) {
    // Prefer sticky session so a mid-refresh state doesn't flip Continue ↔ Sign in again.
    const showContinue = hasSession || state === 'authenticated' || state === 'refreshing';
    return (
      <div className="flex min-h-[60vh] items-center justify-center px-4 py-16">
        <div className="w-full max-w-md text-center">
          <h1 className="text-xl font-semibold text-gray-900">Sign-in didn&apos;t complete</h1>
          <p className="mt-3 text-sm text-gray-600">{error}</p>
          <div className="mt-6 flex flex-col items-center gap-3 sm:flex-row sm:justify-center">
            {showContinue ? (
              <button
                type="button"
                onClick={continueToApp}
                className="inline-flex items-center rounded-md bg-navy-900 px-5 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
              >
                Continue to app
              </button>
            ) : (
              <button
                type="button"
                onClick={() => {
                  // Allow a fresh login attempt after a failed completion.
                  completionPromise = null;
                  void login();
                }}
                className="inline-flex items-center rounded-md bg-navy-900 px-5 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
              >
                Sign in again
              </button>
            )}
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
