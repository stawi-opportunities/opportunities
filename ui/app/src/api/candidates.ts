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
