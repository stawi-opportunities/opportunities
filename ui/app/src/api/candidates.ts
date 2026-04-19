import { getConfig } from "@/utils/config";
import { getAuthRuntime } from "@stawi/auth-runtime";
import type { PlanId } from "@/utils/plans";

function join(base: string, path: string): string {
  const b = base.replace(/\/$/, "");
  const p = path.startsWith("/") ? path : "/" + path;
  return b + p;
}

async function bearer(): Promise<Record<string, string>> {
  try {
    const token = await getAuthRuntime().getAccessToken();
    return token ? { Authorization: `Bearer ${token}` } : {};
  } catch {
    return {};
  }
}

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
  /** CV file (PDF or DOCX). Required — the backend extracts text + AI
   * fields before creating the profile, and matching depends on it. */
  cv: File | null;
}

/**
 * POST /candidates/onboard — creates the candidate profile. The backend
 * expects multipart/form-data so it can optionally receive a CV file and
 * run PDF/DOCX extraction before returning.
 */
export async function submitOnboarding(payload: OnboardingPayload): Promise<Response> {
  const url = join(getConfig().candidatesAPIURL, "/candidates/onboard");
  const form = new FormData();
  form.set("target_job_title",   payload.target_job_title);
  form.set("experience_level",   payload.experience_level);
  form.set("job_search_status",  payload.job_search_status);
  form.set("salary_range",       payload.salary_range ?? "");
  form.set("wants_ats_report",   String(payload.wants_ats_report));
  form.set("preferred_regions",   JSON.stringify(payload.preferred_regions));
  form.set("preferred_timezones", JSON.stringify(payload.preferred_timezones));
  form.set("preferred_languages", JSON.stringify(payload.preferred_languages));
  form.set("job_types",           JSON.stringify(payload.job_types));
  form.set("country",             payload.country);
  form.set("plan",               payload.plan);
  form.set("agree_terms",        String(payload.agree_terms));
  if (payload.cv) form.set("cv", payload.cv);
  return fetch(url, {
    method: "POST",
    headers: { ...(await bearer()) }, // don't set Content-Type — browser adds the boundary
    body: form,
    credentials: "include",
  });
}

export interface MeSubscription {
  /** The server may return legacy "free" or an unknown string; the
   * Dashboard normalises this via `normalizePlan(raw)` — anything not in
   * {starter, pro, managed} means "no active subscription". */
  plan: string | null;
  status: "none" | "active" | "past_due" | "cancelled";
  renews_at?: string;
  agent?: { name: string; email: string } | null;
  queued_matches: number;
  delivered_this_week: number;
}

export interface BillingPlan {
  id: PlanId;
  name: string;
  description: string;
  interval: string;
  /** Amount in minor units of `currency` (cents/shillings). */
  amount: number;
  currency: string;
  /** Always USD cents — useful for showing a "≈ $X" comparison line. */
  usd_cents: number;
}

export type BillingRoute = "POLAR" | "M-PESA" | "AIRTEL" | "MTN";

export interface BillingPlansResponse {
  /** CF-IPCountry (ISO-3166 alpha-2). Empty string if unknown. */
  country: string;
  /** service_payment route — "POLAR" (card/hosted) or one of the
   *  mobile-money rails (STK push to user's phone). */
  route: BillingRoute;
  plans: BillingPlan[];
}

/**
 * GET /billing/plans — returns the geo-resolved plan catalog. No auth
 * required; the frontend calls this before rendering pricing so each
 * user sees the price they'll actually be charged (KES, UGX, NGN, USD).
 */
export async function fetchBillingPlans(): Promise<BillingPlansResponse> {
  const url = join(getConfig().candidatesAPIURL, "/billing/plans");
  const res = await fetch(url, { credentials: "omit" });
  if (!res.ok) {
    throw new Error(`fetchBillingPlans: HTTP ${res.status}`);
  }
  return (await res.json()) as BillingPlansResponse;
}

export type CheckoutStatus = "redirect" | "pending" | "paid" | "failed";

export interface CheckoutResponse {
  /** "redirect" = Polar hosted-checkout URL ready — browser should
   *  303 to redirect_url.
   *  "pending"  = STK push fired / session still queuing — caller
   *  should poll /billing/checkout/status?prompt_id=…
   *  "paid"     = completed synchronously (rare; STK may resolve
   *  inside our short polling window).
   *  "failed"   = see the error field. */
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
  /** E.164 phone number. Required for mobile-money routes
   *  (M-PESA / AIRTEL / MTN); ignored for POLAR. */
  phone?: string;
  /** Optional override: "card", "mpesa", "airtel_money", "mtn_momo". */
  route_hint?: string;
}

/**
 * POST /billing/checkout — asks the candidates service to create a
 * subscription (service_billing) and dispatch a payment prompt
 * (service_payment). The server picks the rail based on CF-IPCountry
 * unless the caller supplies route_hint. See the CheckoutResponse
 * status field for what the caller should do next.
 */
export async function createCheckout(input: CheckoutCreateInput): Promise<CheckoutResponse> {
  const url = join(getConfig().candidatesAPIURL, "/billing/checkout");
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(await bearer()) },
    credentials: "include",
    body: JSON.stringify({
      plan_id:    input.plan_id,
      email:      input.email ?? "",
      phone:      input.phone ?? "",
      route_hint: input.route_hint ?? "",
    }),
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`createCheckout: HTTP ${res.status} ${body}`);
  }
  return (await res.json()) as CheckoutResponse;
}

export interface CheckoutStatusResponse {
  status: CheckoutStatus;
  redirect_url: string;
  subscription_id: string;
  error: string;
}

/**
 * GET /billing/checkout/status?prompt_id=… — long-poll target for
 * flows that don't produce an immediate redirect URL (M-Pesa STK
 * push). Poll every ~2s until status flips to "paid" or "failed".
 */
export async function pollCheckoutStatus(promptId: string): Promise<CheckoutStatusResponse> {
  const url = new URL(join(getConfig().candidatesAPIURL, "/billing/checkout/status"));
  url.searchParams.set("prompt_id", promptId);
  const res = await fetch(url.toString(), {
    headers: { ...(await bearer()) },
    credentials: "include",
  });
  if (!res.ok) {
    throw new Error(`pollCheckoutStatus: HTTP ${res.status}`);
  }
  return (await res.json()) as CheckoutStatusResponse;
}

/**
 * GET /me/subscription — current tier + queue stats. If the endpoint
 * fails (401/404/network) we return a shape that drives the dashboard to
 * show the "complete payment / choose a plan" nudge instead of breaking.
 */
export async function fetchMeSubscription(): Promise<MeSubscription> {
  const fallback: MeSubscription = {
    plan: null,
    status: "none",
    queued_matches: 0,
    delivered_this_week: 0,
    agent: null,
  };
  try {
    const url = join(getConfig().candidatesAPIURL, "/me/subscription");
    const res = await fetch(url, {
      headers: await bearer(),
      credentials: "include",
    });
    if (!res.ok) return fallback;
    const body = (await res.json()) as MeSubscription;
    return { ...fallback, ...body };
  } catch {
    return fallback;
  }
}
