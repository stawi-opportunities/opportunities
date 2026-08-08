import { planById, type PlanId } from '@/utils/plans';
import type { UserStage } from '@/utils/userStage';

/**
 * Dashboard title + plan badge + resolved journey stage chip.
 * Actions (upgrade, find matches, CV) live in the active section content.
 */
export function DashboardHeader({
  plan,
  status,
  stageLabel,
  stageId,
}: {
  plan: PlanId | null;
  status: string;
  /** Canonical journey stage label from useUserContext. */
  stageLabel?: string;
  /** Machine stage id for styling / tooling. */
  stageId?: UserStage;
}) {
  const isFree = !plan || status === 'none';
  const planName = plan ? planById(plan).name : 'Free';
  const stageTone = stageChipTone(stageId);

  return (
    <header className="flex flex-wrap items-center gap-2">
      <h1 className="text-xl font-semibold tracking-tight text-main">Dashboard</h1>
      <span className="rounded-full bg-accent-500/15 px-2.5 py-0.5 text-xs font-medium text-accent-400 ring-1 ring-accent-500/30">
        {isFree ? 'Free' : planName}
      </span>
      {stageLabel && (
        <span
          data-user-stage={stageId}
          className={`rounded-full px-2.5 py-0.5 text-xs font-semibold ring-1 ${stageTone}`}
          title={stageId ? `Stage: ${stageId}` : 'Your current product stage'}
        >
          {stageLabel}
        </span>
      )}
    </header>
  );
}

function stageChipTone(stage?: UserStage): string {
  switch (stage) {
    case 'dashboard_ready':
      return 'bg-emerald-600/15 text-emerald-800 ring-emerald-600/30 dark:text-emerald-200';
    case 'dashboard_setup':
      return 'bg-amber-500/15 text-amber-900 ring-amber-500/40 dark:text-amber-100';
    case 'dashboard_past_due':
      return 'bg-orange-500/15 text-orange-900 ring-orange-500/40 dark:text-orange-100';
    case 'confirming_payment':
      return 'bg-blue-500/15 text-blue-900 ring-blue-500/40 dark:text-blue-100';
    default:
      return 'bg-navy-900/10 text-main ring-muted dark:bg-white/10';
  }
}
