import { planById, type PlanId } from '@/utils/plans';
import type { UserStage } from '@/utils/userStage';
import { Badge } from '@/components/ui/Badge';

/**
 * Dashboard title + plan badge + mobile menu trigger.
 * Stage detail lives in UserStageBanner (only when action is needed).
 * Account avatar is the site Nav profile widget — not duplicated here.
 */
export function DashboardHeader({
  plan,
  status,
  stageLabel,
  stageId,
  onOpenMenu,
}: {
  plan: PlanId | null;
  status: string;
  stageLabel?: string;
  stageId?: UserStage;
  /** Opens the mobile navigation drawer (md:hidden). */
  onOpenMenu?: () => void;
}) {
  const isFree = !plan || status === 'none';
  const planName = plan ? planById(plan).name : 'Free';
  const showStageChip =
    Boolean(stageLabel) && stageId !== 'dashboard_ready' && stageId !== 'loading';

  return (
    <header className="flex flex-wrap items-center gap-x-3 gap-y-2">
      {onOpenMenu && (
        <button
          type="button"
          onClick={onOpenMenu}
          className="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-muted bg-surface text-main hover:bg-surface-hover md:hidden"
          aria-label="Open menu"
        >
          <svg
            className="h-5 w-5"
            fill="none"
            viewBox="0 0 24 24"
            strokeWidth={1.75}
            stroke="currentColor"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5"
            />
          </svg>
        </button>
      )}
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
