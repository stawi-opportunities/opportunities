import type { UserStageInfo } from '@/utils/userStage';

/**
 * Actionable stage notice only — hidden when the seeker is ready so the
 * dashboard stays quiet. Keeps setup / payment / past-due messaging clear.
 */
export function UserStageBanner({
  stage,
  compact = false,
}: {
  stage: UserStageInfo;
  compact?: boolean;
}) {
  if (
    stage.stage === 'loading' ||
    stage.stage === 'anonymous' ||
    stage.stage === 'dashboard_ready'
  ) {
    return null;
  }

  const tone = toneFor(stage.stage);

  return (
    <div
      data-user-stage={stage.stage}
      data-user-stage-home={stage.homePath}
      role="status"
      aria-live="polite"
      aria-label={`${stage.label}. ${stage.summary}`}
      className={`flex flex-wrap items-start gap-2 rounded-lg border px-3.5 py-3 text-sm ${tone.box}`}
    >
      <span
        className={`inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-xs font-semibold ${tone.chip}`}
      >
        {stage.label}
      </span>
      {!compact && <p className={`min-w-0 flex-1 leading-relaxed ${tone.text}`}>{stage.summary}</p>}
    </div>
  );
}

function toneFor(stage: UserStageInfo['stage']): { box: string; chip: string; text: string } {
  switch (stage) {
    case 'onboarding_intake':
      return {
        box: 'border-sky-200/80 bg-sky-50 dark:border-sky-800/50 dark:bg-sky-950/30',
        chip: 'bg-sky-700 text-white',
        text: 'text-sky-950 dark:text-sky-100',
      };
    case 'onboarding_paywall':
      return {
        box: 'border-violet-200/80 bg-violet-50 dark:border-violet-800/50 dark:bg-violet-950/30',
        chip: 'bg-violet-700 text-white',
        text: 'text-violet-950 dark:text-violet-100',
      };
    case 'confirming_payment':
      return {
        box: 'border-blue-200/80 bg-blue-50 dark:border-blue-800/50 dark:bg-blue-950/30',
        chip: 'bg-blue-700 text-white',
        text: 'text-blue-950 dark:text-blue-100',
      };
    case 'dashboard_setup':
      return {
        box: 'border-amber-200/80 bg-amber-50 dark:border-amber-800/50 dark:bg-amber-950/30',
        chip: 'bg-amber-700 text-white',
        text: 'text-amber-950 dark:text-amber-50',
      };
    case 'dashboard_past_due':
      return {
        box: 'border-orange-200/80 bg-orange-50 dark:border-orange-800/50 dark:bg-orange-950/30',
        chip: 'bg-orange-700 text-white',
        text: 'text-orange-950 dark:text-orange-50',
      };
    case 'subscription_error':
      return {
        box: 'border-red-200/80 bg-red-50 dark:border-red-800/50 dark:bg-red-950/30',
        chip: 'bg-red-700 text-white',
        text: 'text-red-950 dark:text-red-50',
      };
    default:
      return {
        box: 'border-muted bg-surface-muted',
        chip: 'bg-navy-800 text-white',
        text: 'text-secondary',
      };
  }
}
