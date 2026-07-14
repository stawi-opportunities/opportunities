import type { PlanId } from '@/utils/plans';
import { authRuntime } from '@/auth/runtime';

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
function isNotFound(err: unknown): boolean {
  const code =
    err && typeof err === 'object' && 'code' in err ? String((err as { code: unknown }).code) : '';
  const msg = err instanceof Error ? err.message : String(err);
  return code === 'API_NOT_FOUND' || /404|not found/i.test(msg);
}

export async function submitOnboarding(
  payload: OnboardingPayload
): Promise<{ id: string; profile_id: string }> {
  // Prefer the canonical /matching prefix; fall back to legacy top-level path.
  try {
    return await authRuntime().fetch('/matching/candidates/onboard', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
  } catch (err) {
    if (!isNotFound(err)) throw err;
    return authRuntime().fetch('/candidates/onboard', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
  }
}

/**
 * PUT /me/cv — uploads the CV via multipart `file`.
 * Server: extract text → files service (or archive) → local CV index →
 * async extract/embed for matching. Response includes extracted_text
 * for immediate chat inference plus file_id when files service stored it.
 */
export interface UploadCVResult {
  ok: boolean;
  cv_length: number;
  filename?: string;
  /** Plain text extracted server-side — feed into chat inference. */
  extracted_text?: string;
  cv_version?: number;
  /** Platform files-service media id when storage is "files". */
  file_id?: string;
  content_uri?: string;
  content_hash?: string;
  /** "files" | "archive" */
  storage?: string;
}

export async function uploadCV(file: File): Promise<UploadCVResult> {
  try {
    return await authRuntime().upload('/matching/me/cv', file);
  } catch (err) {
    if (!isNotFound(err)) throw err;
    return authRuntime().upload('/me/cv', file);
  }
}

/** Current CV document from the local index (files pointer + extracted text). */
export interface MeCVDocument {
  ok: boolean;
  present: boolean;
  cv_version?: number;
  file_id?: string;
  content_uri?: string;
  content_hash?: string;
  filename?: string;
  storage?: string;
  cv_length?: number;
  extracted_text?: string;
}

export async function fetchMeCV(): Promise<MeCVDocument | null> {
  try {
    return await authRuntime().fetch<MeCVDocument>('/matching/me/cv');
  } catch (err) {
    if (!isNotFound(err)) return null;
    try {
      return await authRuntime().fetch<MeCVDocument>('/me/cv');
    } catch {
      return null;
    }
  }
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
    const body = await authRuntime().fetch<{ candidate?: CandidateSummary | null }>('/me');
    return body.candidate ?? null;
  } catch {
    return null;
  }
}

// ── Subscription ──────────────────────────────────────────────────

// ── Settings ───────────────────────────────────────────────────────

export interface ProfilePayload {
  name: string;
  current_title?: string;
  phone?: string;
}

export interface NotificationPrefs {
  email_digest: 'daily' | 'weekly' | 'off';
  match_alerts: boolean;
  weekly_summary: boolean;
  marketing_emails: boolean;
}

/**
 * PUT /me/profile — updates name, title, phone.
 */
export async function updateProfile(payload: ProfilePayload): Promise<{ ok: boolean }> {
  return authRuntime().fetch('/me/profile', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

/**
 * PUT /me/notifications — updates notification preferences.
 */
export async function updateNotificationPrefs(prefs: NotificationPrefs): Promise<{ ok: boolean }> {
  return authRuntime().fetch('/me/notifications', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(prefs),
  });
}

/**
 * POST /me/change-password — change password (requires current password).
 */
export async function changePassword(
  currentPassword: string,
  newPassword: string
): Promise<{ ok: boolean }> {
  return authRuntime().fetch('/me/change-password', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
  });
}

/**
 * POST /me/export-data — request a data-export email.
 */
export async function requestDataExport(): Promise<{ ok: boolean }> {
  return authRuntime().fetch('/me/export-data', { method: 'POST' });
}

/**
 * DELETE /me — permanent account deletion.
 */
export async function deleteAccount(reason?: string): Promise<{ ok: boolean }> {
  return authRuntime().fetch('/me', {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ reason }),
  });
}

export interface MeSubscription {
  plan: string | null;
  status: 'none' | 'active' | 'past_due' | 'cancelled';
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
    status: 'none',
    queued_matches: 0,
    delivered_this_week: 0,
    agent: null,
  };
  try {
    const body = await authRuntime().fetch<MeSubscription>('/me/subscription');
    return { ...fallback, ...body };
  } catch {
    return fallback;
  }
}
