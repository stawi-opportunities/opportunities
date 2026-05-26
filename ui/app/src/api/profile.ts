import type { PlanId } from "@/utils/plans";
import { authRuntime } from "@/auth/runtime";

// Profile & onboarding API calls — all auth'd via @stawi/auth-runtime.
// The runtime owns the JWT; every call uses runtime.fetch() or
// runtime.upload(). Multipart with file + text fields isn't supported,
// which is why onboarding sends profile text via fetch() and the CV
// via a separate upload() call.

// ── Onboarding ────────────────────────────────────────────────────

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
): Promise<{ id: string; profile_id: string }> {
  return authRuntime().fetch("/candidates/onboard", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
}

/**
 * PUT /me/cv — uploads the CV file as the raw request body. The
 * server re-extracts text + scores the CV in the background; the
 * response carries the updated candidate row.
 */
export async function uploadCV(file: File): Promise<{ ok: boolean; cv_length: number }> {
  return authRuntime().upload("/me/cv", file);
}

// ── Candidate profile ─────────────────────────────────────────────

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

/**
 * GET /me — authed user identity + CandidateProfile row. Returns
 * null on any failure so callers can render anon fallback.
 */
export async function fetchCandidate(): Promise<CandidateSummary | null> {
  try {
    const body = await authRuntime().fetch<{ candidate?: CandidateSummary | null }>("/me");
    return body.candidate ?? null;
  } catch {
    return null;
  }
}

// ── Subscription ──────────────────────────────────────────────────

export interface MeSubscription {
  plan: string | null;
  status: "none" | "active" | "past_due" | "cancelled";
  renews_at?: string;
  agent?: { name: string; email: string } | null;
  queued_matches: number;
  delivered_this_week: number;
}

/**
 * GET /me/subscription — auth'd. Fallback shape on any failure so
 * the dashboard renders the "choose a plan" nudge instead of
 * breaking.
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
    const body = await authRuntime().fetch<MeSubscription>("/me/subscription");
    return { ...fallback, ...body };
  } catch {
    return fallback;
  }
}
