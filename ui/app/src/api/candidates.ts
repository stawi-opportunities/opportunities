import type { PlanId } from '@/utils/plans';
import { authRuntime } from '@/auth/runtime';

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
  payload: OnboardingPayload
): Promise<{ id: string; profile_id: string }> {
  return authRuntime().fetch('/matching/candidates/onboard', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

/**
 * PUT /me/cv — uploads the CV file as the raw request body.  The
 * server re-extracts text + scores the CV in the background; the
 * response carries the updated candidate row.
 */
export async function uploadCV(file: File): Promise<{ ok: boolean; cv_length: number }> {
  return authRuntime().upload('/matching/me/cv', file);
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
    const body = await authRuntime().fetch<{ candidate?: CandidateSummary | null }>('/matching/me');
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

export type BillingRoute = 'POLAR' | 'M-PESA' | 'AIRTEL' | 'MTN';

export interface BillingPlansResponse {
  country: string;
  route: BillingRoute;
  plans: BillingPlan[];
}

/** GET /billing/plans — public; no auth.  Use native fetch() for
 *  consistency with the R2-origin calls that don't need a token. */
export async function fetchBillingPlans(): Promise<BillingPlansResponse> {
  const base = getCandidatesOrigin();
  const res = await fetch(`${base}/matching/billing/plans`, { credentials: 'omit' });
  if (!res.ok) throw new Error(`fetchBillingPlans: HTTP ${res.status}`);
  return (await res.json()) as BillingPlansResponse;
}

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
  route_hint?: string;
}

/** POST /billing/checkout — auth'd. */
export async function createCheckout(input: CheckoutCreateInput): Promise<CheckoutResponse> {
  return authRuntime().fetch('/matching/billing/checkout', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      plan_id: input.plan_id,
      email: input.email ?? '',
      phone: input.phone ?? '',
      route_hint: input.route_hint ?? '',
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
  return authRuntime().fetch(
    `/matching/billing/checkout/status?prompt_id=${encodeURIComponent(promptId)}`
  );
}

// ── /me/subscription ─────────────────────────────────────────────

export interface MeSubscription {
  plan: string | null;
  status: 'none' | 'active' | 'past_due' | 'cancelled';
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
    status: 'none',
    queued_matches: 0,
    delivered_this_week: 0,
    agent: null,
  };
  try {
    const body = await authRuntime().fetch<MeSubscription>('/matching/me/subscription');
    return { ...fallback, ...body };
  } catch {
    return fallback;
  }
}

// ── /me/onboarding ─────────────────────────────────────────────

/** The wizard's persisted form values. Shape mirrors the Onboarding.tsx
 *  FormValues minus file (`cv`) and the agree-terms boolean (those are
 *  set on the final submit, not autosaved). Keep field names matching
 *  the form so we can `form.reset(fields)` on resume. */
export interface OnboardingDraftFields {
  target_job_title?: string;
  experience_level?: 'entry' | 'junior' | 'mid' | 'senior' | 'lead' | 'executive';
  job_search_status?: 'actively_looking' | 'open_to_offers' | 'casually_browsing';
  salary_range?: string;
  wants_ats_report?: boolean;
  preferred_regions?: string[];
  preferred_timezones?: string[];
  preferred_languages?: string[];
  job_types?: string[];
  country?: string;
  plan?: 'starter' | 'pro' | 'managed';
}

export interface OnboardingDraft {
  step: 1 | 2 | 3;
  fields: OnboardingDraftFields;
  updated_at?: string;
}

/** GET /matching/me/onboarding — never throws; returns the canonical
 *  empty draft on any failure so the wizard mount is non-blocking. */
export async function fetchOnboardingDraft(): Promise<OnboardingDraft> {
  const empty: OnboardingDraft = { step: 1, fields: {} };
  try {
    const body = await authRuntime().fetch<OnboardingDraft>('/matching/me/onboarding');
    return {
      step: body.step ?? 1,
      fields: body.fields ?? {},
      updated_at: body.updated_at,
    };
  } catch {
    return empty;
  }
}

/** PUT /matching/me/onboarding — fire-and-forget autosave. Errors are
 *  surfaced via the returned promise so the caller can show a
 *  non-blocking warning; we do NOT throw to the caller's `await` in
 *  the happy path. */
export async function saveOnboardingDraft(
  step: 1 | 2 | 3,
  fields: OnboardingDraftFields
): Promise<void> {
  await authRuntime().fetch('/matching/me/onboarding', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ step, fields }),
  });
}

// ── /me/opportunities ─────────────────────────────────────────────

export type OpportunityFilter = 'all' | 'matches' | 'starred' | 'applied';

export interface ApplicationSummary {
  status: 'applied' | 'responded' | 'interview' | 'offer' | 'rejected' | 'hired' | string;
  applied_at: string;
  last_event_at: string;
  method: 'auto' | 'manual' | string;
}

export interface FeedItem {
  opportunity_id: string;
  score?: number;
  starred: boolean;
  application?: ApplicationSummary;
  created_at: string;
}

export interface FeedPage {
  items: FeedItem[];
  next_cursor?: string;
}

export async function fetchOpportunities(
  opts: { filter?: OpportunityFilter; cursor?: string; limit?: number } = {}
): Promise<FeedPage> {
  const params = new URLSearchParams();
  if (opts.filter && opts.filter !== 'all') params.set('filter', opts.filter);
  if (opts.cursor) params.set('cursor', opts.cursor);
  if (opts.limit) params.set('limit', String(opts.limit));
  const query = params.toString();
  const path = `/matching/me/opportunities${query ? `?${query}` : ''}`;
  return await authRuntime().fetch<FeedPage>(path);
}

export async function starOpportunity(opportunityId: string): Promise<void> {
  await authRuntime().fetch('/matching/me/saved-jobs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ opportunity_id: opportunityId }),
  });
}

export async function unstarOpportunity(opportunityId: string): Promise<void> {
  await authRuntime().fetch(`/matching/me/saved-jobs/${encodeURIComponent(opportunityId)}`, {
    method: 'DELETE',
  });
}

export async function applyToOpportunity(
  opportunityId: string,
  method: 'manual' | 'auto' = 'manual'
): Promise<{ application_id: string; status: string; applied_at: string }> {
  return await authRuntime().fetch('/matching/me/applications', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ opportunity_id: opportunityId, method }),
  });
}

// ── helpers ──────────────────────────────────────────────────────

function getCandidatesOrigin(): string {
  // runtime.fetch uses apiBaseUrl; for public endpoints we bypass
  // the runtime entirely and hit the same origin directly.
  // Avoids a circular import to @/utils/config by reading directly.
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
