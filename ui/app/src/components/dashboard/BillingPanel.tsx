import { useState, useEffect } from 'react';
import { planById, type PlanId } from '@/utils/plans';
import { Button } from '@/components/ui/Button';
import { Panel } from './Panel';
import { UsageChart } from './UsageChart';
import { InvoiceHistory } from './InvoiceHistory';
import { fetchUsageHistory, fetchInvoices } from '@/api/billing';
import type { UsageEntry, Invoice } from '@/api/billing';
import type { StringKey } from '@/i18n/strings';

export function BillingPanel({
  plan,
  renewsAt,
  onOpenPlanChange,
  onOpenCancel,
  t,
}: {
  plan: PlanId;
  renewsAt?: string;
  onOpenPlanChange: () => void;
  onOpenCancel: () => void;
  t: (k: StringKey, fallback?: string) => string;
}) {
  const info = planById(plan);
  const [usageHistory, setUsageHistory] = useState<UsageEntry[]>([]);
  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [showUsage, setShowUsage] = useState(false);
  // Payment / subscription history is the primary billing record — open by default.
  const [showInvoices, setShowInvoices] = useState(true);

  useEffect(() => {
    fetchUsageHistory()
      .then(setUsageHistory)
      .catch(() => setUsageHistory([]));
    fetchInvoices()
      .then(setInvoices)
      .catch(() => setInvoices([]));
  }, []);

  return (
    <div className="space-y-4">
      <Panel title={t('nav.billing')}>
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm text-gray-700 dark:text-gray-300">
              <span className="font-medium">{info.name}</span> · ${info.price}/{t('dash.perMonth')}{' '}
              · <span className="text-emerald-700 dark:text-emerald-400">{t('dash.active')}</span>
            </p>
            {renewsAt && (
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {t('dash.renewsOn')} {new Date(renewsAt).toLocaleDateString()}
              </p>
            )}
            <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
              Upgrade or cancel anytime here. Cancellation takes effect at the end of the current
              billing period — you keep access until then.
            </p>
          </div>
          <div className="text-right text-sm font-medium text-gray-900 dark:text-white">
            <p>${info.price}</p>
            <p className="text-xs text-gray-500 dark:text-gray-400">{t('dash.perMonth')}</p>
          </div>
        </div>

        <div className="mt-4 flex flex-wrap gap-2">
          <Button variant="primary" size="sm" type="button" onClick={onOpenPlanChange}>
            {t('dash.changePlan')}
          </Button>
          <Button variant="secondary" size="sm" type="button" onClick={onOpenCancel}>
            {t('cancel.title')}
          </Button>
        </div>

        <div className="mt-4 flex gap-4 border-t border-gray-100 pt-4 dark:border-navy-700">
          <button
            type="button"
            onClick={() => setShowUsage(!showUsage)}
            className="text-sm font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
          >
            {showUsage ? t('common.loading') : t('usage.title')} {showUsage ? '▲' : '▼'}
          </button>
          <button
            type="button"
            onClick={() => setShowInvoices(!showInvoices)}
            className="text-sm font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
          >
            {showInvoices ? t('invoice.title') : t('invoice.title')} {showInvoices ? '▲' : '▼'}
          </button>
        </div>

        {showUsage && (
          <div className="mt-4 border-t border-gray-100 pt-4 dark:border-navy-700">
            <UsageChart history={usageHistory} t={t} />
          </div>
        )}

        {showInvoices && (
          <div className="mt-4 border-t border-gray-100 pt-4 dark:border-navy-700">
            <h3 className="mb-2 text-sm font-medium text-gray-900 dark:text-white">
              Subscription &amp; payment history
            </h3>
            <InvoiceHistory invoices={invoices} t={t} />
          </div>
        )}
      </Panel>
    </div>
  );
}
