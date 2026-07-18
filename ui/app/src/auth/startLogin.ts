/**
 * Kick off the same OIDC flow as the nav Sign-in control.
 * Does not navigate to /onboarding/ first — returnTo is the current page,
 * and /auth/callback/ routes paid → dashboard, unpaid → onboarding.
 */
export async function startLogin(
  login: () => Promise<void>
): Promise<{ ok: true } | { ok: false; message: string }> {
  try {
    await login();
    // Successful redirect never resolves (page unloads). If we return,
    // FedCM completed in-place without a full redirect.
    return { ok: true };
  } catch (err) {
    const msg =
      err instanceof Error && err.message
        ? err.message
        : 'Could not start sign-in. Check your connection and try again.';
    // User dismissals are not hard failures — stay on the page quietly.
    if (/dismiss|abort|cancel/i.test(msg)) {
      return { ok: false, message: '' };
    }
    return { ok: false, message: 'Could not start sign-in. Try again in a moment.' };
  }
}
