/**
 * Modal shell around PreferenceChat for invoke-anywhere refine flows.
 */

import { useCallback, useState } from 'react';
import { authRuntime } from '@/auth/runtime';
import type { OnboardingChatFields, OnboardingChatMessage } from '@/api/candidates';
import { Dialog } from '@/components/common/Dialog';
import { PreferenceChat, type PreferenceChatMode } from './PreferenceChat';
import { chatFieldsToJobPreferences } from './mapFields';

export interface PreferenceChatDialogProps {
  open: boolean;
  onClose: () => void;
  mode?: PreferenceChatMode;
  initialFields?: OnboardingChatFields;
  initialMessages?: OnboardingChatMessage[];
  /** CV already stored — refine must not re-demand upload. */
  cvOnFile?: boolean;
  welcome?: string;
  userName?: string;
  title?: string;
  description?: string;
  onApplied?: (fields: OnboardingChatFields) => void;
  persistPreferences?: boolean;
}

export function PreferenceChatDialog({
  open,
  onClose,
  mode = 'refine',
  initialFields,
  initialMessages,
  cvOnFile = false,
  welcome,
  userName,
  title = 'Tweak what you want',
  description = 'Your conversation is saved. Describe changes in plain language.',
  onApplied,
  persistPreferences = true,
}: PreferenceChatDialogProps) {
  const [applyError, setApplyError] = useState<string | null>(null);

  const handleComplete = useCallback(
    async (fields: OnboardingChatFields) => {
      setApplyError(null);
      if (persistPreferences && mode === 'refine') {
        const job = chatFieldsToJobPreferences(fields);
        await authRuntime().fetch('/candidates/preferences', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ opt_ins: { job } }),
        });
      }
      onApplied?.(fields);
      onClose();
    },
    [mode, onApplied, onClose, persistPreferences]
  );

  return (
    <Dialog open={open} onClose={onClose} title={title} description={description} size="xl">
      {applyError && (
        <p className="mb-3 text-sm text-red-600 dark:text-red-400" role="alert">
          {applyError}
        </p>
      )}
      {open && (
        <PreferenceChat
          key={`${open}-${initialMessages?.length ?? 0}-${initialFields?.target_job_title ?? ''}`}
          mode={mode}
          initialFields={initialFields}
          initialMessages={initialMessages}
          cvOnFile={cvOnFile}
          welcome={welcome}
          userName={userName}
          compact
          minHeightClass="min-h-[18rem] max-h-[60vh]"
          showCompleteAction
          completeLabel="Apply updates"
          onComplete={async (fields) => {
            try {
              await handleComplete(fields);
            } catch (e) {
              setApplyError(
                e instanceof Error && e.message ? e.message : 'Could not save preferences'
              );
              throw e;
            }
          }}
        />
      )}
    </Dialog>
  );
}
