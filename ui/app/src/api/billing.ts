import type { PlanId } from '@/utils/plans';
import { authRuntime } from '@/auth/runtime';

// Billing API — Flutterwave-only checkout.
// Happy path: createCheckout → redirect_url → Flutterwave →
// /dashboard/?billing=success&prompt_id=… → activation + celebrate.

// ── Plans ─────────────────────────────────────────────────────────

export interface BillingPlan {
  id: PlanId;
  name: string;
  description: string;
  interval: string;
  /** Major currency units (10 = US$10). Not cents. */
  amount: number;
  currency: string;
  /** Authoritative price in USD cents (1000 = US$10). Prefer this for display math. */
  usd_cents: number;
}

export type BillingRoute = 'FLUTTERWAVE';

export interface BillingPlansResponse {
  country: string;
  route: BillingRoute;
  plans: BillingPlan[];
}

/**
 * GET /billing/plans — public; no auth. Uses native fetch() so the
 * call bypasses the auth runtime entirely (no token needed).
 */
export async function fetchBillingPlans(): Promise<BillingPlansResponse> {
  const base = getCandidatesOrigin();
  const res = await fetch(`${base}/billing/plans`, { credentials: 'omit' });
  if (!res.ok) throw new Error(`fetchBillingPlans: HTTP ${res.status}`);
  return (await res.json()) as BillingPlansResponse;
}

// ── Checkout ──────────────────────────────────────────────────────

export type CheckoutStatus = 'redirect' | 'pending' | 'paid' | 'failed';

export interface CheckoutResponse {
  status: CheckoutStatus;
  route: BillingRoute;
  redirect_url: string;
  prompt_id: string;
  subscription_id: string;
  amount: number;
  currency: string;
  country: string;
  plan_id: PlanId;
  error: string;
}

export interface CheckoutCreateInput {
  plan_id: PlanId;
  email?: string;
  phone?: string;
}

/** POST /billing/checkout — auth'd. */
export async function createCheckout(input: CheckoutCreateInput): Promise<CheckoutResponse> {
  const body = JSON.stringify({
    plan_id: input.plan_id,
    email: input.email ?? '',
    phone: input.phone ?? '',
  });
  try {
    return await authRuntime().fetch('/matching/billing/checkout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    });
  } catch (err) {
    const code =
      err && typeof err === 'object' && 'code' in err
        ? String((err as { code: unknown }).code)
        : '';
    const msg = err instanceof Error ? err.message : String(err);
    if (code !== 'API_NOT_FOUND' && !/404|not found/i.test(msg)) throw err;
    return authRuntime().fetch('/billing/checkout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    });
  }
}

export interface CheckoutStatusResponse {
  status: CheckoutStatus;
  redirect_url: string;
  subscription_id: string;
  error: string;
}

/** GET /billing/checkout/status?prompt_id=… — auth'd long-poll. */
export async function pollCheckoutStatus(promptId: string): Promise<CheckoutStatusResponse> {
  const path = `/billing/checkout/status?prompt_id=${encodeURIComponent(promptId)}`;
  try {
    return await authRuntime().fetch(`/matching${path}`);
  } catch (err) {
    const code =
      err && typeof err === 'object' && 'code' in err
        ? String((err as { code: unknown }).code)
        : '';
    const msg = err instanceof Error ? err.message : String(err);
    if (code !== 'API_NOT_FOUND' && !/404|not found/i.test(msg)) throw err;
    return authRuntime().fetch(path);
  }
}

// ── Plan change ──────────────────────────────────────────────────

export interface ChangePlanInput {
  plan_id: PlanId;
}

export interface ChangePlanResponse {
  success: boolean;
  new_plan: string;
  prorated_amount: number;
  prorated_credit: number;
  next_billing: string;
}

/** POST /billing/change-plan — auth'd. */
export async function changePlan(input: ChangePlanInput): Promise<ChangePlanResponse> {
  const body = JSON.stringify(input);
  try {
    return await authRuntime().fetch('/matching/billing/change-plan', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    });
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (!/404|not found/i.test(msg)) throw err;
    return authRuntime().fetch('/billing/change-plan', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    });
  }
}

// ── Cancellation ─────────────────────────────────────────────────

export interface CancelInput {
  reason: string;
  detail?: string;
}

export interface CancelResponse {
  success: boolean;
  effective_date: string;
}

/** POST /billing/cancel — auth'd. Cancels at end of current period. */
export async function cancelSubscription(input: CancelInput): Promise<CancelResponse> {
  const body = JSON.stringify(input);
  try {
    return await authRuntime().fetch('/matching/billing/cancel', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    });
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (!/404|not found/i.test(msg)) throw err;
    return authRuntime().fetch('/billing/cancel', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    });
  }
}

// ── Pause / Reactivate ───────────────────────────────────────────

export interface PauseResponse {
  success: boolean;
  resume_date: string;
}

/** POST /billing/pause — auth'd. */
export async function pauseSubscription(): Promise<PauseResponse> {
  return authRuntime().fetch('/billing/pause', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
}

export interface ReactivateResponse {
  success: boolean;
}

/** POST /billing/reactivate — auth'd. */
export async function reactivateSubscription(): Promise<ReactivateResponse> {
  return authRuntime().fetch('/billing/reactivate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
}

// ── Invoice history ──────────────────────────────────────────────

export interface Invoice {
  id: string;
  date: string;
  amount: number;
  currency: string;
  status: 'paid' | 'pending' | 'failed';
  pdf_url?: string;
}

/** GET /billing/invoices — auth'd payment / subscription history. */
export async function fetchInvoices(): Promise<Invoice[]> {
  try {
    return await authRuntime().fetch('/matching/billing/invoices');
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (!/404|not found/i.test(msg)) throw err;
    return authRuntime().fetch('/billing/invoices');
  }
}

// ── Usage history ────────────────────────────────────────────────

export interface UsageEntry {
  week: string;
  delivered: number;
  queued: number;
}

/** GET /billing/usage-history — auth'd. */
export async function fetchUsageHistory(): Promise<UsageEntry[]> {
  try {
    return await authRuntime().fetch('/matching/billing/usage-history');
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (!/404|not found/i.test(msg)) throw err;
    return authRuntime().fetch('/billing/usage-history');
  }
}

// ── Internal helper ───────────────────────────────────────────────

/**
 * Reads candidatesAPIURL from the Hugo-injected <meta name="site-params">
 * tag. Falls back to the production origin so callers always get a
 * valid base URL even before the meta tag hydrates.
 */
export function getCandidatesOrigin(): string {
  const el =
    typeof document !== 'undefined'
      ? document.querySelector<HTMLMetaElement>('meta[name="site-params"]')
      : null;
  if (el) {
    try {
      const d = JSON.parse(el.content) as { candidatesAPIURL?: string };
      if (d.candidatesAPIURL) return d.candidatesAPIURL.replace(/\/$/, '');
    } catch {
      /* fall through */
    }
  }
  return 'https://api.stawi.org';
}
