import { useState } from 'react';
import { useAuth } from '@/providers/AuthProvider';
import { startLogin } from '@/auth/startLogin';

/**
 * Homepage primary CTA: same path as nav Sign in (OIDC), not a hop to
 * /onboarding/ that can hang if auth is down.
 */
export default function HomeCta() {
  const { hasSession, ready, login } = useAuth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Signed-in users are redirected by HomeRedirect; hide the CTA.
  if (hasSession) return null;

  async function onGetStarted() {
    if (busy) return;
    setError(null);
    setBusy(true);
    const result = await startLogin(login);
    if (!result.ok) {
      if (result.message) setError(result.message);
      setBusy(false);
    }
    // If ok and still here (FedCM), land on home so HomeRedirect applies
    // resolveUserStage (entitled → dashboard, unpaid → onboarding).
    if (result.ok) {
      window.location.assign('/');
    }
  }

  return (
    <div className="mt-8 flex flex-col items-center gap-3 sm:flex-row sm:justify-center">
      <a
        href="/search/"
        className="inline-flex min-h-[44px] items-center gap-2 rounded-full bg-accent-500 px-8 py-3.5 text-base font-semibold text-navy-950 shadow-sm transition-colors hover:bg-accent-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2"
      >
        Browse jobs
        <span aria-hidden="true">→</span>
      </a>
      <button
        type="button"
        onClick={() => void onGetStarted()}
        disabled={busy || !ready}
        className="inline-flex min-h-[44px] items-center gap-2 rounded-full border border-muted-strong bg-surface px-8 py-3.5 text-base font-semibold text-main shadow-sm transition hover:border-accent-500/50 hover:text-accent-500 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500 focus-visible:ring-offset-2 disabled:cursor-wait disabled:opacity-70"
      >
        {busy ? 'Signing in…' : 'Get Started'}
        {!busy && <span aria-hidden="true">→</span>}
      </button>
      {error && (
        <p className="max-w-sm text-center text-sm text-red-500" role="alert">
          {error}{' '}
          <button
            type="button"
            className="font-medium underline"
            onClick={() => void onGetStarted()}
          >
            Retry
          </button>
        </p>
      )}
    </div>
  );
}
