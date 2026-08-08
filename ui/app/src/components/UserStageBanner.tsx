import type { UserStageInfo } from '@/utils/userStage';

/**
 * Always-visible stage chip so product and support can see where the user is
 * in the seeker journey (intake / paywall / payment confirm / CV setup / ready).
 */
export function UserStageBanner({
  stage,
  compact = false,
}: {
  stage: UserStageInfo;
  compact?: boolean;
}) {
  if (stage.stage === 'loading' || stage.stage === 'anonymous') {
    return null;
  }

  const tone = toneFor(stage.stage);

  return (
    <div
      data-user-stage={stage.stage}
      role="status"
      aria-live="polite"
      className={`flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2 text-sm ${tone.box}`}
    >
      <span
        className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold tracking-wide ${tone.chip}`}
      >
        {stage.label}
      </span>
      {!compact && <span className={`${tone.text}`}>{stage.summary}</span>}
    </div>
  );
}

function toneFor(stage: UserStageInfo['stage']): { box: string; chip: string; text: string } {
  switch (stage) {
    case 'onboarding_intake':
      return {
        box: 'border-sky-200 bg-sky-50 dark:border-sky-800 dark:bg-sky-950/40',
        chip: 'bg-sky-600 text-white',
        text: 'text-sky-900 dark:text-sky-100',
      };
    case 'onboarding_paywall':
      return {
        box: 'border-violet-200 bg-violet-50 dark:border-violet-800 dark:bg-violet-950/40',
        chip: 'bg-violet-600 text-white',
        text: 'text-violet-900 dark:text-violet-100',
      };
    case 'confirming_payment':
      return {
        box: 'border-blue-200 bg-blue-50 dark:border-blue-800 dark:bg-blue-950/40',
        chip: 'bg-blue-600 text-white',
        text: 'text-blue-900 dark:text-blue-100',
      };
    case 'dashboard_setup':
      return {
        box: 'border-amber-200 bg-amber-50 dark:border-amber-800 dark:bg-amber-950/40',
        chip: 'bg-amber-600 text-white',
        text: 'text-amber-950 dark:text-amber-100',
      };
    case 'dashboard_past_due':
      return {
        box: 'border-orange-300 bg-orange-50 dark:border-orange-800 dark:bg-orange-950/40',
        chip: 'bg-orange-600 text-white',
        text: 'text-orange-950 dark:text-orange-100',
      };
    case 'dashboard_ready':
      return {
        box: 'border-emerald-200 bg-emerald-50 dark:border-emerald-800 dark:bg-emerald-950/40',
        chip: 'bg-emerald-600 text-white',
        text: 'text-emerald-900 dark:text-emerald-100',
      };
    case 'subscription_error':
      return {
        box: 'border-red-200 bg-red-50 dark:border-red-800 dark:bg-red-950/40',
        chip: 'bg-red-600 text-white',
        text: 'text-red-900 dark:text-red-100',
      };
    default:
      return {
        box: 'border-muted bg-surface-muted',
        chip: 'bg-navy-800 text-white',
        text: 'text-secondary',
      };
  }
}
