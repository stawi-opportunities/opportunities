import { planById, type PlanId } from '@/utils/plans';
import type { StringKey } from '@/i18n/strings';
import { Button } from '@/components/ui/Button';

export function DashboardHeader({
  plan,
  status,
  onOpenPlanChange,
  t,
}: {
  plan: PlanId | null;
  status: string;
  onOpenPlanChange?: () => void;
  t: (k: StringKey, fallback?: string) => string;
}) {
  const isFree = !plan || status === 'none';
  const planName = plan ? planById(plan).name : 'Free';

  return (
    <header className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex items-center gap-2">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Dashboard</h1>
        <span className="rounded-full bg-gray-100 px-2.5 py-0.5 text-xs font-medium text-gray-700 dark:bg-navy-700 dark:text-gray-200">
          {isFree ? 'Free' : planName}
        </span>
      </div>
      <div className="flex items-center gap-2">
        <a
          href="/search/"
          className="text-sm font-medium text-gray-600 hover:text-navy-900 dark:text-gray-300"
        >
          Jobs
        </a>
        {plan !== 'managed' && (
          <Button
            variant="primary"
            size="sm"
            type="button"
            onClick={() => {
              if (isFree || !plan) {
                window.location.hash = 'billing';
                return;
              }
              onOpenPlanChange?.();
            }}
          >
            {status === 'active' || status === 'trial' ? t('dash.changePlan') : t('dash.viewPlans')}
          </Button>
        )}
      </div>
    </header>
  );
}
