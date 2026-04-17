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
  country: string;
  plan: PlanId;
  agree_terms: boolean;
  /** Optional CV file (PDF or DOCX). When present the backend extracts
   * text + AI-structured fields and stores them alongside the profile. */
  cv?: File | null;
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
  form.set("preferred_regions",  JSON.stringify(payload.preferred_regions));
  form.set("preferred_timezones", JSON.stringify(payload.preferred_timezones));
  form.set("country",            payload.country);
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
  plan: PlanId;
  status: "none" | "active" | "past_due" | "cancelled";
  renews_at?: string;
  agent?: { name: string; email: string } | null;
  queued_matches: number;
  delivered_this_week: number;
}

/**
 * GET /me/subscription — current tier + queue stats. The backend endpoint
 * is not wired yet; on 401/404/network failure we degrade to the free-tier
 * view without breaking the UI.
 */
export async function fetchMeSubscription(): Promise<MeSubscription> {
  const fallback: MeSubscription = {
    plan: "free",
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
