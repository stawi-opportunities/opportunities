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
    // If ok and still here (FedCM), go to onboarding / dashboard via full load.
    if (result.ok) {
      window.location.assign('/onboarding/');
    }
  }

  return (
    <div className="mt-8 flex flex-col items-center gap-3">
      <button
        type="button"
        onClick={() => void onGetStarted()}
        disabled={busy || !ready}
        className="inline-flex items-center gap-2 rounded-full bg-navy-900 px-8 py-3.5 text-base font-semibold text-white shadow-sm transition hover:bg-navy-800 focus:outline-none focus-visible:ring-2 focus-visible:ring-navy-900 focus-visible:ring-offset-2 disabled:cursor-wait disabled:opacity-70"
      >
        {busy ? 'Signing in…' : 'Get Started'}
        {!busy && <span aria-hidden="true">→</span>}
      </button>
      {error && (
        <p className="max-w-sm text-center text-sm text-red-600" role="alert">
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
      <a
        href="/search/"
        className="text-sm text-gray-500 underline-offset-2 hover:text-gray-800 hover:underline"
      >
        Or browse jobs
      </a>
    </div>
  );
}
