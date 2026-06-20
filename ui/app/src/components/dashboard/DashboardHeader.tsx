import { planById, type PlanId } from '@/utils/plans';
import type { StringKey } from '@/i18n/strings';

const STATUS_STYLES: Record<string, { bg: string; labelKey: StringKey }> = {
  active: { bg: 'bg-emerald-50 text-emerald-700', labelKey: 'dash.statusActive' },
  past_due: { bg: 'bg-amber-50 text-amber-700', labelKey: 'dash.statusPastDue' },
  cancelled: { bg: 'bg-red-50 text-red-700', labelKey: 'dash.statusCancelled' },
};

export function DashboardHeader({
  plan,
  status,
  t,
}: {
  plan: PlanId | null;
  status: string;
  t: (k: StringKey, fallback?: string) => string;
}) {
  const style = STATUS_STYLES[status] ?? { bg: 'bg-gray-100 text-gray-600', labelKey: 'dash.setupIncomplete' as StringKey };
  const planName = plan ? planById(plan).name : 'Setup incomplete';
  const tagline =
    plan && status === 'active'
      ? planById(plan).tagline
      : status === 'past_due'
        ? 'Update your payment details to resume matching.'
        : status === 'cancelled'
          ? 'Re-activate any time to start receiving matches again.'
          : status === 'none'
            ? 'Finish payment to unlock matching.'
            : '';
  return (
    <header className="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-3xl font-bold text-gray-900">{t('dash.title')}</h1>
        <p className="mt-1 flex items-center gap-2 text-gray-600">
          <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${style.bg}`}>
            {plan ? planName : t(style.labelKey)}
          </span>
          <span>{tagline}</span>
        </p>
      </div>
      <div className="flex items-center gap-3">
        <a href="/jobs/" className="text-sm font-medium text-gray-700 hover:text-navy-900">
          {t('dash.browseJobs')}
        </a>
        {plan !== 'managed' && (
          <a
            href="/pricing/"
            className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
          >
            {status === 'active' ? t('dash.changePlan') : t('dash.viewPlans')}
          </a>
        )}
      </div>
    </header>
  );
}
