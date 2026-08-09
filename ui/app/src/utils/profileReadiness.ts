/**
 * Whether a candidate has enough profile + CV signal for job matching.
 * Aligns with onboarding chat required fields and placement_ready on the server.
 *
 * Two bars (keep UI surfaces function-specific):
 * - matchCapable — CV/capabilities on file → Matches can run Find matches
 * - ready — full chat prefs + CV → “profile complete” on CV hub / paywall
 */

import type { MeCVDocument } from '@/api/profile';
import type { OnboardingChatFields, OnboardingDraftFields } from '@/api/candidates';
import { isChatReady, missingChatFields } from '@/onboarding/chatHeuristic';
import { draftToChatFields } from '@/components/preference-chat';

/** Chat fields that are preferences/filters, not the CV upload itself. */
export const PREFERENCE_FIELD_KEYS = [
  'target_job_title',
  'job_types',
  'salary_expectation',
  'preferred_countries',
  'experience_level',
] as const;

export type ProfileReadiness = {
  /**
   * Full matching profile: placement ready, or chat prefs + CV.
   * Use on CV hub / onboarding paywall — not to block the Matches page.
   */
  ready: boolean;
  /**
   * Enough material to score roles (CV file, extract, or capabilities).
   * Drives journey stage dashboard_ready and Matches page CV probes.
   */
  matchCapable: boolean;
  /** Server placement document is complete for matching. */
  placementReady: boolean;
  /** CV file or extracted text is on file. */
  cvPresent: boolean;
  /** Onboarding chat required fields satisfied (client heuristic). */
  chatReady: boolean;
  /** All missing chat keys (capabilities + preferences). */
  missing: string[];
  /** Preference gaps only — never includes capabilities/CV. */
  preferenceMissing: string[];
};

/**
 * Merge CV text into chat fields the same way onboarding does so readiness
 * is consistent whether they uploaded a file or pasted in chat.
 */
export function mergeCVIntoFields(
  fields: OnboardingChatFields,
  cv: MeCVDocument | null | undefined
): OnboardingChatFields {
  if (!cv) return fields;
  let f = { ...fields };
  if (cv.extracted_text?.trim()) {
    const text = cv.extracted_text.trim();
    if (!f.extra_info || f.extra_info.length < text.length) {
      f = { ...f, extra_info: text };
    }
  } else if (cv.present && !f.extra_info?.trim()) {
    f = {
      ...f,
      extra_info:
        'Uploaded CV on file. Resume document stored for matching (experience, education, skills).',
    };
  }
  return f;
}

export function evaluateProfileReadiness(
  cv: MeCVDocument | null | undefined,
  draft?: OnboardingDraftFields | null
): ProfileReadiness {
  const fields = mergeCVIntoFields(draftToChatFields(draft ?? {}), cv);
  const missing = missingChatFields(fields);
  const chatReady = isChatReady(fields);
  const cvPresent = Boolean(cv?.present || cv?.extracted_text?.trim());
  const placementReady = cv?.placement_ready === true;
  const capabilitiesMissing = missing.includes('capabilities');
  const preferenceMissing = missing.filter((m) => m !== 'capabilities');

  // Server placement_ready is authoritative. Also accept chat-complete + CV
  // so a user who just finished chat is not stuck if placement rebuild lags.
  const ready = placementReady || (chatReady && cvPresent);
  // Matches can run once we have a CV/capabilities signal — incomplete salary
  // or countries must not force “upload CV” on the Matches page.
  const matchCapable = placementReady || cvPresent || !capabilitiesMissing;

  return {
    ready,
    matchCapable,
    placementReady,
    cvPresent,
    chatReady,
    missing,
    preferenceMissing,
  };
}

/** Path to onboarding chat when the dashboard gate fires. */
export const ONBOARDING_CHAT_PATH = '/onboarding/';

/** Human labels for missing preference keys (Matches/CV copy). */
export function preferenceMissingLabels(keys: string[]): string[] {
  const map: Record<string, string> = {
    target_job_title: 'target role',
    job_types: 'job types',
    salary_expectation: 'salary expectation',
    preferred_countries: 'preferred countries',
    experience_level: 'experience level',
    capabilities: 'CV / capabilities',
  };
  return keys.map((k) => map[k] ?? k.replace(/_/g, ' '));
}
