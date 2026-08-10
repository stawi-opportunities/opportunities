import { planById, type PlanId } from '@/utils/plans';
import type { UserStage } from '@/utils/userStage';
import { Badge } from '@/components/ui/Badge';

/**
 * Dashboard title + plan badge. Stage detail lives in UserStageBanner
 * (only when action is needed) to keep the header uncluttered.
 */
export function DashboardHeader({
  plan,
  status,
  stageLabel,
  stageId,
}: {
  plan: PlanId | null;
  status: string;
  stageLabel?: string;
  stageId?: UserStage;
}) {
  const isFree = !plan || status === 'none';
  const planName = plan ? planById(plan).name : 'Free';
  const showStageChip =
    Boolean(stageLabel) && stageId !== 'dashboard_ready' && stageId !== 'loading';

  return (
    <header className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <h1 className="text-xl font-semibold tracking-tight text-main sm:text-2xl">Dashboard</h1>
      <Badge variant={isFree ? 'neutral' : 'accent'}>{isFree ? 'Free' : planName}</Badge>
      {showStageChip && stageLabel && (
        <Badge
          variant={stageBadgeVariant(stageId)}
          data-user-stage={stageId}
          title={stageId ? `Stage: ${stageId}` : undefined}
        >
          {stageLabel}
        </Badge>
      )}
    </header>
  );
}

function stageBadgeVariant(
  stage?: UserStage
): 'success' | 'warning' | 'info' | 'neutral' | 'accent' {
  switch (stage) {
    case 'dashboard_ready':
      return 'success';
    case 'dashboard_setup':
    case 'dashboard_past_due':
      return 'warning';
    case 'confirming_payment':
      return 'info';
    default:
      return 'neutral';
  }
}
