/**
 * Whether a candidate has enough profile + CV signal for job matching.
 * Aligns with onboarding chat required fields and placement_ready on the server.
 */

import type { MeCVDocument } from '@/api/profile';
import type { OnboardingChatFields, OnboardingDraftFields } from '@/api/candidates';
import { isChatReady, missingChatFields } from '@/onboarding/chatHeuristic';
import { draftToChatFields } from '@/components/preference-chat';

export type ProfileReadiness = {
  ready: boolean;
  /** Server placement document is complete for matching. */
  placementReady: boolean;
  /** CV file or extracted text is on file. */
  cvPresent: boolean;
  /** Onboarding chat required fields satisfied (client heuristic). */
  chatReady: boolean;
  missing: string[];
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

  // Server placement_ready is authoritative. Also accept chat-complete + CV
  // so a user who just finished chat is not stuck if placement rebuild lags.
  const ready = placementReady || (chatReady && cvPresent);

  return {
    ready,
    placementReady,
    cvPresent,
    chatReady,
    missing,
  };
}

/** Path to onboarding chat when the dashboard gate fires. */
export const ONBOARDING_CHAT_PATH = '/onboarding/';
