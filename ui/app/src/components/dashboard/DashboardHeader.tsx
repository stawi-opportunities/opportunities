import { planById, type PlanId } from '@/utils/plans';

/**
 * Dashboard title + plan badge only.
 * Actions (upgrade, find matches, CV) live in the active section content.
 */
export function DashboardHeader({ plan, status }: { plan: PlanId | null; status: string }) {
  const isFree = !plan || status === 'none';
  const planName = plan ? planById(plan).name : 'Free';

  return (
    <header className="flex flex-wrap items-center gap-2">
      <h1 className="text-xl font-semibold tracking-tight text-main">Dashboard</h1>
      <span className="rounded-full bg-accent-500/15 px-2.5 py-0.5 text-xs font-medium text-accent-400 ring-1 ring-accent-500/30">
        {isFree ? 'Free' : planName}
      </span>
    </header>
  );
}
