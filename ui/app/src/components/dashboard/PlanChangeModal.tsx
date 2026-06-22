import { useState } from 'react';
import { PLANS, planById, type PlanId } from '@/utils/plans';
import { changePlan } from '@/api/billing';
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
            <h2 className="text-lg font-semibold text-gray-900">{t('plan.changeTitle')}</h2>
            <p className="text-sm text-gray-600">
              {t('plan.currentPlan')}:{' '}
              <span className="font-medium">
                {currentInfo.name} — ${currentInfo.price}/mo
              </span>
            </p>
            {currentPlan === 'managed' ? (
              <div className="rounded-md bg-amber-50 p-4 text-sm text-amber-800">
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
                          ? 'cursor-not-allowed border-gray-200 bg-gray-50 opacity-60'
                          : isSelected
                            ? 'border-accent-500 bg-accent-50 ring-1 ring-accent-500'
                            : 'border-gray-200 hover:border-gray-300 hover:bg-gray-50'
                      }`}
                    >
                      <div>
                        <p className="font-medium text-gray-900">{p.name}</p>
                        <p className="text-sm text-gray-500">{p.tagline}</p>
                        <div className="mt-1 flex flex-wrap gap-1">
                          {p.features.slice(0, 3).map((f) => (
                            <span
                              key={f}
                              className="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-700"
                            >
                              {f}
                            </span>
                          ))}
                        </div>
                      </div>
                      <div className="ml-4 text-right">
                        <p className="text-lg font-bold text-gray-900">${p.price}</p>
                        <p className="text-xs text-gray-500">/mo</p>
                      </div>
                    </button>
                  );
                })}
              </div>
            )}
            <div className="flex justify-end gap-3 pt-4">
              <button
                type="button"
                onClick={onClose}
                className="rounded-md px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              >
                {t('cta.cancel')}
              </button>
              {currentPlan !== 'managed' && (
                <button
                  type="button"
                  onClick={handleContinue}
                  disabled={selected === currentPlan}
                  className="rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800 disabled:opacity-50"
                >
                  {t('plan.confirmChange')}
                </button>
              )}
            </div>
          </div>
        );

      case 'confirm':
        return (
          <div className="space-y-4">
            <h2 className="text-lg font-semibold text-gray-900">{t('plan.confirmChange')}</h2>
            <div className="rounded-lg border border-gray-200 bg-gray-50 p-4">
              <div className="flex items-center justify-between text-sm">
                <span className="text-gray-600">{t('plan.currentPlan')}</span>
                <span className="font-medium text-gray-900">
                  {currentInfo.name} — ${currentInfo.price}/mo
                </span>
              </div>
              <div className="mt-2 flex items-center justify-between text-sm">
                <span className="text-gray-600">{t('plan.newPlan')}</span>
                <span className="font-medium text-accent-700">
                  {planById(selected).name} — ${planById(selected).price}/mo
                </span>
              </div>
            </div>
            <div className="flex justify-end gap-3 pt-4">
              <button
                type="button"
                onClick={() => setStep('select')}
                className="rounded-md px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              >
                {t('cta.cancel')}
              </button>
              <button
                type="button"
                onClick={() => void handleConfirm()}
                className="rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
              >
                {t('plan.confirmChange')}
              </button>
            </div>
          </div>
        );

      case 'success':
        return (
          <div className="space-y-4 text-center">
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-emerald-100">
              <svg
                className="h-6 w-6 text-emerald-600"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <path strokeLinecap="round" strokeLinejoin="round" d="m4.5 12.75 6 6 9-13.5" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-gray-900">
              {t('plan.changeSuccess').replace('{plan}', planById(selected).name)}
            </h2>
            {prorated > 0 && (
              <p className="text-sm text-gray-500">
                {t('plan.proratedAmount')}: ${(prorated / 100).toFixed(2)}
              </p>
            )}
            {nextBilling && (
              <p className="text-sm text-gray-500">
                {t('plan.nextBilling')}: {new Date(nextBilling).toLocaleDateString()}
              </p>
            )}
            <button
              type="button"
              onClick={onClose}
              className="rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
            >
              {t('cta.close')}
            </button>
          </div>
        );

      case 'error':
        return (
          <div className="space-y-4 text-center">
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-red-100">
              <svg
                className="h-6 w-6 text-red-600"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-gray-900">{t('plan.changeError')}</h2>
            {errorMsg && <p className="text-sm text-red-600">{errorMsg}</p>}
            <div className="flex justify-center gap-3">
              <button
                type="button"
                onClick={() => setStep('select')}
                className="rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
              >
                {t('cta.retry')}
              </button>
              <button
                type="button"
                onClick={onClose}
                className="rounded-md px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              >
                {t('cta.close')}
              </button>
            </div>
          </div>
        );
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4">
      <div className="w-full max-w-lg rounded-xl bg-white shadow-xl">
        <div className="px-6 py-5">{renderContent()}</div>
      </div>
    </div>
  );
}
