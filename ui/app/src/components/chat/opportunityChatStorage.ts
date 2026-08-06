/**
 * Client-side persistence for the shared multi-job opportunity chat transcript.
 * Server chat-agent keeps one session per candidate; this mirrors the SPA rail.
 */

import type { OnboardingChatFields, OnboardingChatMessage } from '@/api/candidates';
import type { OpportunityChatCardData } from './OpportunityChatCard';

export type OpportunityChatMessage = OnboardingChatMessage & {
  card?: OpportunityChatCardData;
};

const STORAGE_KEY = 'stawi.opportunity.chat.v1';

export type OpportunityChatStore = {
  messages: OpportunityChatMessage[];
  fields: OnboardingChatFields;
  updated_at: string;
};

export function loadOpportunityChat(): OpportunityChatStore | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as OpportunityChatStore;
    if (!Array.isArray(parsed.messages)) return null;
    return {
      messages: parsed.messages,
      fields: parsed.fields ?? {},
      updated_at: parsed.updated_at ?? '',
    };
  } catch {
    return null;
  }
}

export function saveOpportunityChat(store: OpportunityChatStore): void {
  if (typeof window === 'undefined') return;
  try {
    sessionStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        ...store,
        updated_at: new Date().toISOString(),
      })
    );
  } catch {
    /* quota / private mode */
  }
}
