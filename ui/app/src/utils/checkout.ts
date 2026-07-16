import { createCheckout, type CheckoutCreateInput, type CheckoutResponse } from '@/api/billing';
import { authRuntime } from '@/auth/runtime';

export const PENDING_PROMPT_KEY = 'stawi.billing.pending_prompt_id';

/** Email from OIDC claims for Flutterwave customer_email. */
export async function checkoutEmailFromAuth(): Promise<string> {
  try {
    const claims = await authRuntime().getClaims();
    return String(claims?.email ?? '').trim();
  } catch {
    return '';
  }
}

export function stashPendingPrompt(promptId: string | undefined | null): void {
  if (!promptId) return;
  try {
    localStorage.setItem(PENDING_PROMPT_KEY, promptId);
  } catch {
    /* private mode */
  }
}

export function clearPendingPrompt(): void {
  try {
    localStorage.removeItem(PENDING_PROMPT_KEY);
  } catch {
    /* ignore */
  }
}

/**
 * Start checkout and leave the SPA for Flutterwave.
 *
 * Navigation priority (strict):
 *   1. Any non-empty redirect_url → Flutterwave pay page (always)
 *   2. paid → dashboard success
 *   3. failed / missing URL → throw so the caller can show the error
 *   4. pending without URL → dashboard poller (last-resort recovery only)
 */
export async function startCheckoutAndNavigate(
  input: CheckoutCreateInput
): Promise<CheckoutResponse> {
  const email = input.email?.trim() || (await checkoutEmailFromAuth());
  const res = await createCheckout({ ...input, email: email || undefined });

  if (res.prompt_id) {
    stashPendingPrompt(res.prompt_id);
  }

  // Normalize possible response shapes from the gateway / auth runtime.
  const payURL = (res.redirect_url || '').trim();

  // 1. Happy path — always prefer a pay URL when present, regardless of status.
  if (payURL && !isOurReturnURL(payURL)) {
    window.location.assign(payURL);
    return res;
  }

  if (res.status === 'paid') {
    clearPendingPrompt();
    const q = res.prompt_id
      ? `?billing=success&prompt_id=${encodeURIComponent(res.prompt_id)}`
      : '?billing=success';
    window.location.assign(`/dashboard/${q}`);
    return res;
  }

  if (res.status === 'failed' || res.error) {
    clearPendingPrompt();
    throw new Error(res.error || 'Checkout failed. Please try again.');
  }

  // 2. Rare recovery — URL still materialising after the server short-poll.
  if (res.status === 'pending' && res.prompt_id) {
    window.location.assign(
      `/dashboard/?billing=pending&prompt_id=${encodeURIComponent(res.prompt_id)}`
    );
    return res;
  }

  clearPendingPrompt();
  throw new Error(res.error || 'Payment page was not ready. Please try again.');
}

/** True when the URL is our SPA return landing, not Flutterwave's pay page. */
function isOurReturnURL(u: string): boolean {
  return (
    u.includes('billing=success') ||
    u.includes('/dashboard/?billing=') ||
    u.includes('/dashboard?billing=')
  );
}
