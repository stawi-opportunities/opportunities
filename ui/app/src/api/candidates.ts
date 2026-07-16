import type { PlanId } from '@/utils/plans';
import { authRuntime } from '@/auth/runtime';

// All auth'd candidate-service API calls go through the shared runtime.
// @stawi/auth-runtime 1.0+ owns the token (no getAccessToken export),
// so we can't forge Bearer headers ourselves ΓÇö every call uses
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
 * POST /candidates/onboard ΓÇö creates the CandidateProfile row from
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
 * PUT /me/cv ΓÇö uploads the CV file as the raw request body.  The
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

/** GET /me ΓÇö authed user identity + CandidateProfile row.  Returns
 *  null on any failure so callers can render anon fallback. */
export async function fetchCandidate(): Promise<CandidateSummary | null> {
  try {
    const body = await authRuntime().fetch<{ candidate?: CandidateSummary | null }>('/matching/me');
    return body.candidate ?? null;
  } catch {
    return null;
  }
}

// ΓöÇΓöÇ Billing ΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇ

export interface BillingPlan {
  id: PlanId;
  name: string;
  description: string;
  interval: string;
  amount: number;
  currency: string;
  usd_cents: number;
}

export type BillingRoute = 'FLUTTERWAVE';

export interface BillingPlansResponse {
  country: string;
  route: BillingRoute;
  plans: BillingPlan[];
}

/** GET /billing/plans ΓÇö public; no auth.  Use native fetch() for
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
    }),
  });
}

export interface CheckoutStatusResponse {
  status: CheckoutStatus;
  redirect_url: string;
  subscription_id: string;
  error: string;
}

/** GET /billing/checkout/status?prompt_id=ΓÇª ΓÇö auth'd long-poll. */
export async function pollCheckoutStatus(promptId: string): Promise<CheckoutStatusResponse> {
  return authRuntime().fetch(
    `/matching/billing/checkout/status?prompt_id=${encodeURIComponent(promptId)}`
  );
}

// ΓöÇΓöÇ /me/subscription ΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇ

export interface MeSubscription {
  plan: string | null;
  status: 'none' | 'active' | 'past_due' | 'cancelled' | 'trial';
  renews_at?: string;
  agent?: { name: string; email: string } | null;
  queued_matches: number;
  delivered_this_week: number;
}

/**
 * GET /me/subscription — auth'd.
 * Throws on failure so callers can distinguish unpaid from network errors
 * (mapping errors to status "none" re-prompted paid users to pay again).
 */
export async function fetchMeSubscription(): Promise<MeSubscription> {
  const body = await authRuntime().fetch<MeSubscription>('/matching/me/subscription');
  return {
    plan: body.plan ?? null,
    status: body.status ?? 'none',
    renews_at: body.renews_at,
    agent: body.agent ?? null,
    queued_matches: body.queued_matches ?? 0,
    delivered_this_week: body.delivered_this_week ?? 0,
  };
}

// ΓöÇΓöÇ /me/onboarding ΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇ

/** The wizard's persisted form values. Shape mirrors the Onboarding.tsx
 *  FormValues minus file (`cv`) and the agree-terms boolean (those are
 *  set on the final submit, not autosaved). Keep field names matching
 *  the form so we can `form.reset(fields)` on resume. */
export interface OnboardingDraftFields {
  target_job_title?: string;
  experience_level?: 'entry' | 'junior' | 'mid' | 'senior' | 'lead' | 'executive';
  job_search_status?: 'actively_looking' | 'open_to_offers' | 'casually_browsing';
  salary_range?: string;
  salary_min?: number;
  salary_max?: number;
  currency?: string;
  wants_ats_report?: boolean;
  preferred_regions?: string[];
  preferred_countries?: string[];
  preferred_timezones?: string[];
  preferred_languages?: string[];
  job_types?: string[];
  country?: string;
  linkedin?: string;
  extra_info?: string;
  plan?: 'starter' | 'pro' | 'managed';
}

export interface OnboardingChatMessage {
  role: 'user' | 'assistant';
  content: string;
}

export interface OnboardingDraft {
  step: 1 | 2 | 3;
  fields: OnboardingDraftFields;
  /** Persisted preference-chat transcript (always available for resume/refine). */
  messages?: OnboardingChatMessage[];
  updated_at?: string;
}

/** GET /matching/me/onboarding — never throws; returns the canonical
 *  empty draft on any failure so the wizard mount is non-blocking. */
export async function fetchOnboardingDraft(): Promise<OnboardingDraft> {
  const empty: OnboardingDraft = { step: 1, fields: {}, messages: [] };
  try {
    const body = await authRuntime().fetch<OnboardingDraft>('/matching/me/onboarding');
    return {
      step: body.step ?? 1,
      fields: body.fields ?? {},
      messages: Array.isArray(body.messages) ? body.messages : [],
      updated_at: body.updated_at,
    };
  } catch {
    return empty;
  }
}

/** PUT /matching/me/onboarding — fire-and-forget autosave. When messages
 *  are omitted the server keeps the stored transcript. */
export async function saveOnboardingDraft(
  step: 1 | 2 | 3,
  fields: OnboardingDraftFields,
  messages?: OnboardingChatMessage[]
): Promise<void> {
  await authRuntime().fetch('/matching/me/onboarding', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      step,
      fields,
      ...(messages ? { messages } : {}),
    }),
  });
}

// ── /me/chat ──────────────────────────────────────────────────────────────
// Shared preference / intake conversation. Onboarding, dashboard refine, and
// opportunity side-chat all embed the same widget and hit this endpoint.

export interface OnboardingChatFields {
  target_job_title?: string;
  experience_level?: string;
  job_search_status?: string;
  salary_min?: number | null;
  salary_max?: number | null;
  currency?: string;
  preferred_regions?: string[];
  /** Countries to source / notify opportunities from. */
  preferred_countries?: string[];
  preferred_timezones?: string[];
  preferred_languages?: string[];
  /** Kinds of jobs to notify about (Full-time, Contract, …). */
  job_types?: string[];
  country?: string;
  /** LinkedIn URL or handle — optional only; does not unlock readiness. */
  linkedin?: string;
  /** CV paste / skills summary (required for capabilities). */
  extra_info?: string;
}

export interface OnboardingChatFieldStatus {
  ok: boolean;
  value?: string;
  reason?: string;
}

export interface OnboardingChatResponse {
  reply: string;
  fields: OnboardingChatFields;
  missing: string[];
  ready: boolean;
  /** Full transcript after this turn (server-persisted when Drafts are wired). */
  messages?: OnboardingChatMessage[];
  /** Per-required-field assessment from the server (authoritative). */
  field_status?: Record<string, OnboardingChatFieldStatus>;
  /** How fields were filled: llm | heuristic | llm+heuristic */
  source?: string;
  /** Combined qualifications + preferences document used for matching. */
  placement_summary?: string;
  placement_ready?: boolean;
}

export type SendMeChatInput = {
  message: string;
  history?: OnboardingChatMessage[];
  draft?: OnboardingChatFields;
  /** Structured LinkedIn from composer (optional). */
  linkedin?: string;
  /** Extracted CV plain text from file picker (optional). */
  cv_text?: string;
  cv_filename?: string;
};

/**
 * POST /matching/me/chat — one conversational turn.
 * Server merges free-text (or pasted CV) into structured fields via AI
 * when inference is configured, otherwise a heuristic parser.
 *
 * Falls back to a client-side heuristic when the matching binary is older
 * (404) or temporarily unavailable so the embedded chat still works.
 */
export async function sendMeChat(input: SendMeChatInput): Promise<OnboardingChatResponse> {
  try {
    return await authRuntime().fetch<OnboardingChatResponse>('/matching/me/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        message: input.message,
        history: input.history ?? [],
        draft: input.draft ?? {},
        linkedin: input.linkedin ?? '',
        cv_text: input.cv_text ?? '',
        cv_filename: input.cv_filename ?? '',
      }),
      // LLM turns can be slow on free-tier inference.
      timeoutMs: 90_000,
    });
  } catch (err) {
    // Local fallback when matching hasn't been redeployed yet, or inference
    // is briefly unavailable. Auth errors still surface to the UI.
    const code =
      err && typeof err === 'object' && 'code' in err
        ? String((err as { code: unknown }).code)
        : '';
    const msg = err instanceof Error ? err.message : String(err);
    const fallbackable =
      code === 'API_NOT_FOUND' ||
      code === 'API_SERVER_ERROR' ||
      code === 'NETWORK_ERROR' ||
      code === 'NETWORK_TIMEOUT' ||
      /404|502|503|504|Failed to fetch|not found|timeout/i.test(msg);
    if (!fallbackable) throw err;
    const { localChatTurn } = await import('@/onboarding/chatHeuristic');
    const seed: OnboardingChatFields = { ...(input.draft ?? {}) };
    if (input.linkedin?.trim()) seed.linkedin = input.linkedin.trim();
    if (input.cv_text?.trim()) {
      seed.extra_info = seed.extra_info
        ? `${seed.extra_info}\n\n${input.cv_text.trim()}`
        : input.cv_text.trim();
    }
    const message =
      input.message?.trim() ||
      (input.cv_text
        ? `I've attached my CV (${input.cv_filename || 'CV'}).`
        : input.linkedin
          ? `My LinkedIn is ${input.linkedin}`
          : '');
    const res = localChatTurn(message, seed, input.history ?? []);
    // Best-effort persist so the next page load can resume the same thread.
    try {
      const { fieldsToDraft } = await import('@/components/preference-chat/mapFields');
      await saveOnboardingDraft(res.ready ? 2 : 1, fieldsToDraft(res.fields), res.messages);
    } catch {
      // non-blocking
    }
    return res;
  }
}

// ── /me/opportunities ΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇ

export type OpportunityFilter = 'all' | 'matches' | 'starred' | 'applied';

export interface ApplicationSummary {
  status: 'applied' | 'responded' | 'interview' | 'offer' | 'rejected' | 'hired' | string;
  applied_at: string;
  last_event_at: string;
  method: 'auto' | 'manual' | string;
}

export interface FeedItem {
  opportunity_id: string;
  apply_url?: string;
  score?: number;
  starred: boolean;
  application?: ApplicationSummary;
  created_at: string;
  /** Card enrichment from matching feed join (preferred over public snapshot). */
  slug?: string;
  title?: string;
  kind?: string;
  company?: string;
  country?: string;
  region?: string;
  city?: string;
  remote?: boolean;
  posted_at?: string;
  salary_min?: number;
  salary_max?: number;
  currency?: string;
  has_how_to_apply?: boolean;
}

export interface FeedPage {
  items: FeedItem[];
  next_cursor?: string;
}

export async function fetchOpportunities(
  opts: { filter?: OpportunityFilter; cursor?: string; limit?: number; sort?: string } = {}
): Promise<FeedPage> {
  const params = new URLSearchParams();
  if (opts.filter && opts.filter !== 'all') params.set('filter', opts.filter);
  if (opts.cursor) params.set('cursor', opts.cursor);
  if (opts.limit) params.set('limit', String(opts.limit));
  if (opts.sort) params.set('sort', opts.sort);
  const query = params.toString();
  const path = `/matching/me/opportunities${query ? `?${query}` : ''}`;
  return await authRuntime().fetch<FeedPage>(path);
}

/** Paywalled application instructions for one opportunity (auth required). */
export interface ApplyDetails {
  opportunity_id: string;
  slug: string;
  apply_url: string;
  how_to_apply?: string;
  /** True when authenticated but not on an active/trial plan. */
  locked?: boolean;
}

/**
 * GET /matching/me/opportunities/{id}/apply — returns how_to_apply only for
 * active subscribers; free/cancelled candidates get `{ locked: true }`.
 */
export async function fetchApplyDetails(idOrSlug: string): Promise<ApplyDetails> {
  return authRuntime().fetch<ApplyDetails>(
    `/matching/me/opportunities/${encodeURIComponent(idOrSlug)}/apply`
  );
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

/** Response from POST /matching/me/matches/refresh (on-demand gap-fill). */
export interface MatchRefreshResult {
  ok: boolean;
  matches_written: number;
  opps_scanned: number;
  run_id?: string;
  min_score?: number;
}

/**
 * POST /matching/me/matches/refresh — re-run reverse-KNN gap-fill for the
 * authenticated paid candidate. Powers "Find matches now" on the dashboard.
 */
export async function refreshMyMatches(): Promise<MatchRefreshResult> {
  try {
    return await authRuntime().fetch<MatchRefreshResult>('/matching/me/matches/refresh', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
      timeoutMs: 60_000,
    });
  } catch (err) {
    if (!isNotFound(err)) throw err;
    return authRuntime().fetch<MatchRefreshResult>('/matching/api/me/matches/refresh', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({}),
      timeoutMs: 60_000,
    });
  }
}

function isNotFound(err: unknown): boolean {
  const code =
    err && typeof err === 'object' && 'code' in err ? String((err as { code: unknown }).code) : '';
  const msg = err instanceof Error ? err.message : String(err);
  return code === 'API_NOT_FOUND' || /404|not found/i.test(msg);
}

// ΓöÇΓöÇ helpers ΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇ

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
