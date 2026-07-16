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
  cancelAtPeriodEnd,
  onOpenPlanChange,
  onOpenCancel,
  t,
}: {
  plan: PlanId;
  renewsAt?: string;
  cancelAtPeriodEnd?: boolean;
  onOpenPlanChange: () => void;
  onOpenCancel: () => void;
  t: (k: StringKey, fallback?: string) => string;
}) {
  const info = planById(plan);
  const [usageHistory, setUsageHistory] = useState<UsageEntry[]>([]);
  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [showUsage, setShowUsage] = useState(false);
  const [showInvoices, setShowInvoices] = useState(true);
  const [historyLoading, setHistoryLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setHistoryLoading(true);
    Promise.all([
      fetchUsageHistory().catch(() => [] as UsageEntry[]),
      fetchInvoices().catch(() => [] as Invoice[]),
    ]).then(([usage, inv]) => {
      if (cancelled) return;
      setUsageHistory(usage);
      setInvoices(inv);
      setHistoryLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const periodLabel = renewsAt
    ? new Date(renewsAt).toLocaleDateString(undefined, {
        year: 'numeric',
        month: 'long',
        day: 'numeric',
      })
    : null;

  return (
    <div className="space-y-4">
      <Panel title={t('nav.billing')}>
        {cancelAtPeriodEnd && (
          <div
            className="mb-4 rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-900/30 dark:text-amber-200"
            role="status"
          >
            <p className="font-medium">Cancellation scheduled</p>
            <p className="mt-0.5">
              You keep full access
              {periodLabel ? ` until ${periodLabel}` : ' until the end of your billing period'}. No
              further charges after that.
            </p>
          </div>
        )}

        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0 flex-1">
            <p className="text-sm text-gray-700 dark:text-gray-300">
              <span className="font-medium text-gray-900 dark:text-white">{info.name}</span>
              <span className="text-gray-500 dark:text-gray-400">
                {' '}
                · ${info.price}/{t('dash.perMonth')}
              </span>
              {' · '}
              {cancelAtPeriodEnd ? (
                <span className="font-medium text-amber-700 dark:text-amber-300">
                  Ends {periodLabel ?? 'this period'}
                </span>
              ) : (
                <span className="font-medium text-emerald-700 dark:text-emerald-400">
                  {t('dash.active')}
                </span>
              )}
            </p>
            {periodLabel && !cancelAtPeriodEnd && (
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {t('dash.renewsOn')} {periodLabel}
              </p>
            )}
            <p className="mt-2 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
              Manage your plan here. Upgrades apply immediately. If you cancel, access continues
              until the end of the period you already paid for.
            </p>
          </div>
          <div className="text-right text-sm font-medium text-gray-900 dark:text-white">
            <p className="text-2xl font-semibold">${info.price}</p>
            <p className="text-xs font-normal text-gray-500 dark:text-gray-400">
              {t('dash.perMonth')}
            </p>
          </div>
        </div>

        <div className="mt-4 flex flex-wrap gap-2">
          <Button variant="primary" size="sm" type="button" onClick={onOpenPlanChange}>
            {t('dash.changePlan')}
          </Button>
          {!cancelAtPeriodEnd && (
            <Button variant="secondary" size="sm" type="button" onClick={onOpenCancel}>
              {t('cancel.title')}
            </Button>
          )}
        </div>

        <div className="mt-4 flex flex-wrap gap-4 border-t border-gray-100 pt-4 dark:border-navy-700">
          <button
            type="button"
            onClick={() => setShowUsage(!showUsage)}
            className="text-sm font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
          >
            {t('usage.title')} {showUsage ? '▲' : '▼'}
          </button>
          <button
            type="button"
            onClick={() => setShowInvoices(!showInvoices)}
            className="text-sm font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
          >
            Payment history {showInvoices ? '▲' : '▼'}
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
              Payment history
            </h3>
            <p className="mb-3 text-xs text-gray-500 dark:text-gray-400">
              Every subscription checkout and payment attempt for your account.
            </p>
            {historyLoading ? (
              <p className="text-sm text-gray-500 dark:text-gray-400">{t('common.loading')}</p>
            ) : (
              <InvoiceHistory invoices={invoices} t={t} />
            )}
          </div>
        )}
      </Panel>
    </div>
  );
}
