import { planById, type PlanId } from '@/utils/plans';
import type { StringKey } from '@/i18n/strings';

import { Button } from '@/components/ui/Button';

const STATUS_STYLES: Record<string, { bg: string; labelKey: StringKey }> = {
  active: {
    bg: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
    labelKey: 'dash.statusActive',
  },
  past_due: {
    bg: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300',
    labelKey: 'dash.statusPastDue',
  },
  cancelled: {
    bg: 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300',
    labelKey: 'dash.statusCancelled',
  },
};

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
  const defaultBg = 'bg-gray-100 text-gray-600 dark:bg-navy-700 dark:text-gray-300';
  const style = STATUS_STYLES[status] ?? {
    bg: defaultBg,
    labelKey: 'dash.setupIncomplete' as StringKey,
  };
  const isFree = !plan || status === 'none';
  const planName = plan ? planById(plan).name : 'Free proof';
  const tagline =
    plan && (status === 'active' || status === 'trial')
      ? planById(plan).tagline
      : status === 'past_due'
        ? 'Update your payment details to resume matching.'
        : status === 'cancelled'
          ? 'Re-activate any time to start receiving matches again.'
          : isFree
            ? 'Free matches & tools first — subscribe when the shortlist is worth it.'
            : '';
  return (
    <header className="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-3xl font-bold text-gray-900 dark:text-white">{t('dash.title')}</h1>
        <p className="mt-1 flex items-center gap-2 text-gray-600 dark:text-gray-400">
          <span
            className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
              isFree
                ? 'bg-accent-50 text-accent-800 dark:bg-accent-900/30 dark:text-accent-300'
                : style.bg
            }`}
          >
            {isFree ? 'Free proof' : planName}
          </span>
          <span>{tagline}</span>
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <span className="group relative inline-flex" title="No notifications">
          <span className="flex h-10 w-10 items-center justify-center rounded-md text-gray-400 transition-colors hover:bg-gray-50 hover:text-gray-600 dark:hover:bg-navy-800 dark:hover:text-gray-300">
            <svg
              className="h-5 w-5"
              fill="none"
              viewBox="0 0 24 24"
              strokeWidth={1.5}
              stroke="currentColor"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M14.857 17.082a23.848 23.848 0 0 0 5.454-1.31A8.967 8.967 0 0 1 18 9.75V9A6 6 0 0 0 6 9v.75a8.967 8.967 0 0 1-2.312 6.022c1.733.64 3.56 1.085 5.455 1.31m5.714 0a24.255 24.255 0 0 1-5.714 0m5.714 0a3 3 0 1 1-5.714 0"
              />
            </svg>
          </span>
        </span>
        <a
          href="/jobs/"
          className="text-sm font-medium text-gray-700 hover:text-navy-900 dark:text-gray-300 dark:hover:text-white"
        >
          {t('dash.browseJobs')}
        </a>
        {plan !== 'managed' && (
          <Button
            variant="primary"
            size="sm"
            type="button"
            onClick={() => {
              // Unpaid: go to billing section (plan picker), not PlanChangeModal (needs active plan).
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
