import { useEffect, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { PLANS, planById, type PlanId } from '@/utils/plans';
import { changePlan } from '@/api/billing';
import { useFocusTrap } from '@/hooks/useFocusTrap';
import type { StringKey } from '@/i18n/strings';

type Step = 'select' | 'confirm' | 'success' | 'error';

export function PlanChangeModal({
  currentPlan,
  onClose,
  t,
  onSuccess,
}: {
  currentPlan: PlanId;
  onClose: () => void;
  t: (k: StringKey, fallback?: string) => string;
  onSuccess: () => void;
}) {
  const [step, setStep] = useState<Step>('select');
  const [selected, setSelected] = useState<PlanId>(currentPlan);
  const [prorated, setProrated] = useState(0);
  const [nextBilling, setNextBilling] = useState('');
  const [errorMsg, setErrorMsg] = useState('');

  const currentInfo = planById(currentPlan);
  const dialogRef = useRef<HTMLDivElement | null>(null);

  useFocusTrap(dialogRef, true, onClose);

  useEffect(() => {
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = '';
    };
  }, []);

  const handleContinue = () => {
    if (selected === currentPlan) return;
    setStep('confirm');
  };

  const handleConfirm = async () => {
    try {
      const res = await changePlan({ plan_id: selected });
      setProrated(res.prorated_amount);
      setNextBilling(res.next_billing);
      setStep('success');
      onSuccess();
    } catch {
      setErrorMsg(t('plan.changeError'));
      setStep('error');
    }
  };

  const renderContent = () => {
    switch (step) {
      case 'select':
        return (
          <div className="space-y-4">
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
              {t('plan.changeTitle')}
            </h2>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              {t('plan.currentPlan')}:{' '}
              <span className="font-medium">
                {currentInfo.name} — ${currentInfo.price}/mo
              </span>
            </p>
            {currentPlan === 'managed' ? (
              <div className="rounded-md bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
                {t('plan.managedChangeHint')}
              </div>
            ) : (
              <div className="mt-4 space-y-3">
                {PLANS.filter((p) => p.id !== 'managed').map((p) => {
                  const isCurrent = p.id === currentPlan;
                  const isSelected = p.id === selected;
                  return (
                    <button
                      key={p.id}
                      type="button"
                      disabled={isCurrent}
                      onClick={() => setSelected(p.id)}
                      className={`flex w-full items-center justify-between rounded-lg border p-4 text-left transition-colors ${
                        isCurrent
                          ? 'cursor-not-allowed border-gray-200 bg-gray-50 opacity-60 dark:border-navy-600 dark:bg-navy-800'
                          : isSelected
                            ? 'border-accent-500 bg-accent-50 ring-1 ring-accent-500 dark:border-accent-600 dark:bg-accent-900/20'
                            : 'border-gray-200 hover:border-gray-300 hover:bg-gray-50 dark:border-navy-700 dark:hover:border-navy-600 dark:hover:bg-navy-800'
                      }`}
                    >
                      <div>
                        <p className="font-medium text-gray-900 dark:text-white">{p.name}</p>
                        <p className="text-sm text-gray-500 dark:text-gray-400">{p.tagline}</p>
                        <div className="mt-1 flex flex-wrap gap-1">
                          {p.features.slice(0, 3).map((f) => (
                            <span
                              key={f}
                              className="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-700 dark:bg-navy-700 dark:text-gray-300"
                            >
                              {f}
                            </span>
                          ))}
                        </div>
                      </div>
                      <div className="ml-4 text-right">
                        <p className="text-lg font-bold text-gray-900 dark:text-white">
                          ${p.price}
                        </p>
                        <p className="text-xs text-gray-500 dark:text-gray-400">/mo</p>
                      </div>
                    </button>
                  );
                })}
              </div>
            )}
            <div className="flex justify-end gap-3 pt-4">
              <Button variant="secondary" size="md" type="button" onClick={onClose}>
                {t('cta.cancel')}
              </Button>
              {currentPlan !== 'managed' && (
                <Button
                  variant="primary"
                  size="md"
                  type="button"
                  onClick={handleContinue}
                  disabled={selected === currentPlan}
                >
                  {t('plan.confirmChange')}
                </Button>
              )}
            </div>
          </div>
        );

      case 'confirm':
        return (
          <div className="space-y-4">
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
              {t('plan.confirmChange')}
            </h2>
            <div className="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-navy-700 dark:bg-navy-800">
              <div className="flex items-center justify-between text-sm">
                <span className="text-gray-600 dark:text-gray-400">{t('plan.currentPlan')}</span>
                <span className="font-medium text-gray-900 dark:text-white">
                  {currentInfo.name} — ${currentInfo.price}/mo
                </span>
              </div>
              <div className="mt-2 flex items-center justify-between text-sm">
                <span className="text-gray-600 dark:text-gray-400">{t('plan.newPlan')}</span>
                <span className="font-medium text-accent-700 dark:text-accent-300">
                  {planById(selected).name} — ${planById(selected).price}/mo
                </span>
              </div>
            </div>
            <div className="flex justify-end gap-3 pt-4">
              <Button variant="secondary" size="md" type="button" onClick={() => setStep('select')}>
                {t('cta.cancel')}
              </Button>
              <Button
                variant="primary"
                size="md"
                type="button"
                onClick={() => void handleConfirm()}
              >
                {t('plan.confirmChange')}
              </Button>
            </div>
          </div>
        );

      case 'success':
        return (
          <div className="space-y-4 text-center">
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-emerald-100 dark:bg-emerald-900/30">
              <svg
                className="h-6 w-6 text-emerald-600 dark:text-emerald-400"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <path strokeLinecap="round" strokeLinejoin="round" d="m4.5 12.75 6 6 9-13.5" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
              {t('plan.changeSuccess').replace('{plan}', planById(selected).name)}
            </h2>
            {prorated > 0 && (
              <p className="text-sm text-gray-500 dark:text-gray-400">
                {t('plan.proratedAmount')}: ${(prorated / 100).toFixed(2)}
              </p>
            )}
            {nextBilling && (
              <p className="text-sm text-gray-500 dark:text-gray-400">
                {t('plan.nextBilling')}: {new Date(nextBilling).toLocaleDateString()}
              </p>
            )}
            <Button variant="primary" size="md" type="button" onClick={onClose}>
              {t('cta.close')}
            </Button>
          </div>
        );

      case 'error':
        return (
          <div className="space-y-4 text-center">
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-red-100 dark:bg-red-900/30">
              <svg
                className="h-6 w-6 text-red-600 dark:text-red-400"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
              {t('plan.changeError')}
            </h2>
            {errorMsg && <p className="text-sm text-red-600 dark:text-red-400">{errorMsg}</p>}
            <div className="flex justify-center gap-3">
              <Button variant="primary" size="md" type="button" onClick={() => setStep('select')}>
                {t('cta.retry')}
              </Button>
              <Button variant="secondary" size="md" type="button" onClick={onClose}>
                {t('cta.close')}
              </Button>
            </div>
          </div>
        );
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4">
      <div
        ref={dialogRef}
        className="w-full max-w-lg rounded-xl bg-white shadow-xl dark:bg-navy-900"
      >
        <div className="px-6 py-5">{renderContent()}</div>
      </div>
    </div>
  );
}
