import { useEffect, useState } from 'react';
import { planById, type PlanId } from '@/utils/plans';
import { Button } from '@/components/ui/Button';
import { Panel } from './Panel';
import { InvoiceHistory } from './InvoiceHistory';
import { fetchInvoices, type Invoice } from '@/api/billing';
import type { StringKey } from '@/i18n/strings';

/**
 * Subscription: current plan + change/cancel, plus invoices & receipts.
 */
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
  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [historyLoading, setHistoryLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setHistoryLoading(true);
    fetchInvoices()
      .catch(() => [] as Invoice[])
      .then((inv) => {
        if (cancelled) return;
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
    <div className="space-y-6">
      <Panel title={t('settings.sectionSubscription')}>
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
            <p className="text-sm text-main">
              <span className="font-medium">{info.name}</span>
              <span className="text-secondary">
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
              <p className="mt-1 text-xs text-secondary">
                {t('dash.renewsOn')} {periodLabel}
              </p>
            )}
            <p className="mt-2 text-xs leading-relaxed text-secondary">
              Upgrade or downgrade anytime. Cancel keeps access until the period you already paid
              for ends.
            </p>
          </div>
          <div className="text-right text-sm font-medium text-main">
            <p className="text-2xl font-semibold">${info.price}</p>
            <p className="text-xs font-normal text-secondary">{t('dash.perMonth')}</p>
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
      </Panel>

      <Panel title={t('invoice.receiptsTitle')}>
        <p className="mb-3 text-xs text-secondary">
          Payment history for your account. Download a receipt when a PDF link is available.
        </p>
        {historyLoading ? (
          <p className="text-sm text-secondary">{t('common.loading')}</p>
        ) : (
          <InvoiceHistory invoices={invoices} t={t} />
        )}
      </Panel>
    </div>
  );
}
