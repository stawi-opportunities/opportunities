import { createCheckout, type CheckoutCreateInput, type CheckoutResponse } from '@/api/billing';

export const PENDING_PROMPT_KEY = 'stawi.billing.pending_prompt_id';

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
 * Start checkout and open hosted pay.stawi.org.
 *
 * Hosted checkout binds payment to **profile contacts** from ProfileService
 * only (email → card; phone → MoMo or card). Do not invent payer contact
 * details from OIDC claims or free-text that is not on the profile.
 *
 * Navigation priority (strict):
 *   1. Any non-empty redirect_url → pay.stawi.org (always)
 *   2. paid → dashboard success
 *   3. failed / missing URL → throw so the caller can show the error
 *   4. pending without URL → dashboard poller (last-resort recovery only)
 */
export async function startCheckoutAndNavigate(
  input: CheckoutCreateInput
): Promise<CheckoutResponse> {
  // Omit invented email/phone — pay.stawi.org loads contacts from the profile.
  const rest = { ...input };
  delete rest.email;
  delete rest.phone;
  let res: CheckoutResponse;
  try {
    res = await createCheckout(rest);
  } catch (e) {
    // Already subscribed → send them to the dashboard, not a new pay page.
    const msg = e instanceof Error ? e.message : String(e);
    if (/already_subscribed|already have an active/i.test(msg)) {
      clearPendingPrompt();
      window.location.assign('/dashboard/#billing');
      throw e;
    }
    throw e;
  }

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
