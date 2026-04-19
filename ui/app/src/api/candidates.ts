import type { PlanId } from "@/utils/plans";
import { authRuntime } from "@/auth/runtime";

// All auth'd candidate-service API calls go through the shared runtime.
// @stawi/auth-runtime 1.0+ owns the token (no getAccessToken export),
// so we can't forge Bearer headers ourselves — every call uses
// `runtime.fetch()` (JSON / string / ArrayBuffer bodies only) or
// `runtime.upload()` (single-file body). Multipart with file + text
// fields isn't supported, which is why onboarding sends profile text
// via fetch() and the CV via a separate upload() call.

/** Matches the Go onboardBody shape (JSON, no file). */
export interface OnboardingPayload {
  target_job_title: string;
  experience_level: string;
  job_search_status: string;
  salary_range?: string;
  wants_ats_report: boolean;
  preferred_regions: string[];
  preferred_timezones: string[];
  preferred_languages: string[];
  job_types: string[];
  country: string;
  plan: PlanId;
  agree_terms: boolean;
  salary_min?: number;
  salary_max?: number;
  us_work_auth?: boolean | null;
  needs_sponsorship?: boolean | null;
  currency?: string;
}

/**
 * POST /candidates/onboard — creates the CandidateProfile row from
 * the text fields. CV upload is a separate PUT /me/cv call (required
 * because the v1 runtime can't send multipart-with-text-fields).
 */
export async function submitOnboarding(
  payload: OnboardingPayload,
): Promise<{ id: number; profile_id: string }> {
  return authRuntime().fetch("/candidates/onboard", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

/**
 * PUT /me/cv — uploads the CV file as the raw request body.  The
 * server re-extracts text + scores the CV in the background; the
 * response carries the updated candidate row.
 */
export async function uploadCV(file: File): Promise<{ ok: boolean; cv_length: number }> {
  return authRuntime().upload("/me/cv", file);
}

/** Subset of CandidateProfile the UI consumes. */
export interface CandidateSummary {
  profile_id: string;
  status: string;
  current_title: string;
  preferred_countries: string;
  preferred_regions: string;
  remote_preference: string;
  languages: string;
  plan_id: string;
  subscription: string;
}

/** GET /me — authed user identity + CandidateProfile row.  Returns
 *  null on any failure so callers can render anon fallback. */
export async function fetchCandidate(): Promise<CandidateSummary | null> {
  try {
    const body = await authRuntime().fetch<{ candidate?: CandidateSummary | null }>("/me");
    return body.candidate ?? null;
  } catch {
    return null;
  }
}

// ── Billing ──────────────────────────────────────────────────────

export interface BillingPlan {
  id: PlanId;
  name: string;
  description: string;
  interval: string;
  amount: number;
  currency: string;
  usd_cents: number;
}

export type BillingRoute = "POLAR" | "M-PESA" | "AIRTEL" | "MTN";

export interface BillingPlansResponse {
  country: string;
  route: BillingRoute;
  plans: BillingPlan[];
}

/** GET /billing/plans — public; no auth.  Use native fetch() for
 *  consistency with the R2-origin calls that don't need a token. */
export async function fetchBillingPlans(): Promise<BillingPlansResponse> {
  const base = getCandidatesOrigin();
  const res = await fetch(`${base}/billing/plans`, { credentials: "omit" });
  if (!res.ok) throw new Error(`fetchBillingPlans: HTTP ${res.status}`);
  return (await res.json()) as BillingPlansResponse;
}

export type CheckoutStatus = "redirect" | "pending" | "paid" | "failed";

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
  route_hint?: string;
}

/** POST /billing/checkout — auth'd. */
export async function createCheckout(input: CheckoutCreateInput): Promise<CheckoutResponse> {
  return authRuntime().fetch("/billing/checkout", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      plan_id:    input.plan_id,
      email:      input.email ?? "",
      phone:      input.phone ?? "",
      route_hint: input.route_hint ?? "",
    }),
  });
}

export interface CheckoutStatusResponse {
  status: CheckoutStatus;
  redirect_url: string;
  subscription_id: string;
  error: string;
}

/** GET /billing/checkout/status?prompt_id=… — auth'd long-poll. */
export async function pollCheckoutStatus(promptId: string): Promise<CheckoutStatusResponse> {
  return authRuntime().fetch(`/billing/checkout/status?prompt_id=${encodeURIComponent(promptId)}`);
}

// ── /me/subscription ─────────────────────────────────────────────

export interface MeSubscription {
  plan: string | null;
  status: "none" | "active" | "past_due" | "cancelled";
  renews_at?: string;
  agent?: { name: string; email: string } | null;
  queued_matches: number;
  delivered_this_week: number;
}

/** GET /me/subscription — auth'd.  Fallback shape on any failure so
 *  the dashboard renders the "choose a plan" nudge instead of
 *  breaking. */
export async function fetchMeSubscription(): Promise<MeSubscription> {
  const fallback: MeSubscription = {
    plan: null,
    status: "none",
    queued_matches: 0,
    delivered_this_week: 0,
    agent: null,
  };
  try {
    const body = await authRuntime().fetch<MeSubscription>("/me/subscription");
    return { ...fallback, ...body };
  } catch {
    return fallback;
  }
}

// ── helpers ──────────────────────────────────────────────────────

function getCandidatesOrigin(): string {
  // runtime.fetch uses apiBaseUrl; for public endpoints we bypass
  // the runtime entirely and hit the same origin directly.
  // Avoids a circular import to @/utils/config by reading directly.
  const el = typeof document !== "undefined"
    ? document.querySelector<HTMLMetaElement>('meta[name="site-params"]')
    : null;
  if (el) {
    try {
      const d = JSON.parse(el.content) as { candidatesAPIURL?: string };
      if (d.candidatesAPIURL) return d.candidatesAPIURL.replace(/\/$/, "");
    } catch { /* fall through */ }
  }
  return "https://api.stawi.org";
}
