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
 * Start checkout and leave the SPA for Flutterwave (happy path).
 *
 * Required SPA steps only:
 *   1. POST /billing/checkout
 *   2. If redirect_url → assign (Flutterwave)
 *   3. Else if pending + prompt_id → dashboard poller (rare recovery)
 *   4. Else if paid → dashboard success
 *   5. Else throw
 */
export async function startCheckoutAndNavigate(
  input: CheckoutCreateInput
): Promise<CheckoutResponse> {
  const email = input.email?.trim() || (await checkoutEmailFromAuth());
  const res = await createCheckout({ ...input, email: email || undefined });

  if (res.prompt_id) {
    stashPendingPrompt(res.prompt_id);
  }

  // Step 2 — happy path: one hop to Flutterwave.
  if (res.redirect_url && (res.status === 'redirect' || res.status === 'pending')) {
    window.location.assign(res.redirect_url);
    return res;
  }
  if (res.status === 'redirect' && !res.redirect_url) {
    throw new Error(res.error || 'Checkout URL was not ready. Please try again.');
  }
  // Step 3 — rare: URL still materialising; poller opens it when ready.
  if (res.status === 'pending' && res.prompt_id) {
    window.location.assign(
      `/dashboard/?billing=pending&prompt_id=${encodeURIComponent(res.prompt_id)}`
    );
    return res;
  }
  // Step 4 — already paid (edge case).
  if (res.status === 'paid') {
    clearPendingPrompt();
    const q = res.prompt_id
      ? `?billing=success&prompt_id=${encodeURIComponent(res.prompt_id)}`
      : '?billing=success';
    window.location.assign(`/dashboard/${q}`);
    return res;
  }

  clearPendingPrompt();
  throw new Error(res.error || 'Checkout did not complete.');
}
