/**
 * Dashboard-scoped host: one dialog + openRefine() for QuickActions,
 * PreferencesPanel, ProfileCompleteness, etc.
 */

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { useQueryClient } from '@tanstack/react-query';
import {
  fetchOnboardingDraft,
  type OnboardingChatFields,
  type OnboardingChatMessage,
} from '@/api/candidates';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import { QUERY_KEYS } from '@/constants/queryKeys';
import { PreferenceChatDialog } from './PreferenceChatDialog';
import { profileToChatFields } from './mapFields';

interface PreferenceChatHostValue {
  openRefine: (seed?: OnboardingChatFields) => void;
  close: () => void;
  isOpen: boolean;
}

const PreferenceChatHostContext = createContext<PreferenceChatHostValue | null>(null);

export function usePreferenceChat(): PreferenceChatHostValue {
  const ctx = useContext(PreferenceChatHostContext);
  if (!ctx) {
    throw new Error('usePreferenceChat must be used within PreferenceChatHost');
  }
  return ctx;
}

/** Safe variant for components that may render outside the host (no-ops). */
export function usePreferenceChatOptional(): PreferenceChatHostValue | null {
  return useContext(PreferenceChatHostContext);
}

export function PreferenceChatHost({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [seed, setSeed] = useState<OnboardingChatFields | undefined>();
  const [messages, setMessages] = useState<OnboardingChatMessage[]>([]);
  const profileQ = useCandidateProfile();
  const qc = useQueryClient();

  const openRefine = useCallback(
    (override?: OnboardingChatFields) => {
      if (override) {
        setSeed(override);
        setMessages([]);
        setOpen(true);
        return;
      }
      setSeed(profileToChatFields(profileQ.data));
      setMessages([]);
      setOpen(true);
      // Resume full conversation + fields from the server session.
      void fetchOnboardingDraft().then((draft) => {
        setSeed(profileToChatFields(profileQ.data, draft.fields));
        setMessages(draft.messages ?? []);
      });
    },
    [profileQ.data]
  );

  const close = useCallback(() => setOpen(false), []);

  const value = useMemo(
    () => ({ openRefine, close, isOpen: open }),
    [openRefine, close, open]
  );

  return (
    <PreferenceChatHostContext.Provider value={value}>
      {children}
      <PreferenceChatDialog
        open={open}
        onClose={close}
        mode="refine"
        initialFields={seed}
        initialMessages={messages}
        onApplied={() => {
          void qc.invalidateQueries({ queryKey: QUERY_KEYS.CANDIDATE_PROFILE });
        }}
      />
    </PreferenceChatHostContext.Provider>
  );
}
