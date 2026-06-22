import { useState } from 'react';
import { cancelSubscription } from '@/api/billing';
import type { StringKey } from '@/i18n/strings';

type Step = 'reason' | 'confirm' | 'success' | 'error';

const REASONS = [
  { id: 'found_job', key: 'cancel.foundJob' as StringKey },
  { id: 'too_expensive', key: 'cancel.tooExpensive' as StringKey },
  { id: 'not_enough', key: 'cancel.notEnoughMatches' as StringKey },
  { id: 'pausing', key: 'cancel.justPausing' as StringKey },
  { id: 'other', key: 'cancel.other' as StringKey },
];

const REASON_MESSAGES: Record<string, StringKey> = {
  found_job: 'cancel.foundJobMessage',
  too_expensive: 'cancel.tooExpensiveHint',
  not_enough: 'cancel.tooExpensiveHint',
  pausing: 'cancel.pauseHint',
};

export function CancelSubscriptionModal({
  onClose,
  t,
  onSuccess,
}: {
  onClose: () => void;
  t: (k: StringKey, fallback?: string) => string;
  onSuccess: () => void;
}) {
  const [step, setStep] = useState<Step>('reason');
  const [selectedReason, setSelectedReason] = useState('');
  const [detail, setDetail] = useState('');
  const [checked, setChecked] = useState(false);
  const [errorMsg, setErrorMsg] = useState('');

  const handleContinue = () => {
    if (!selectedReason) return;
    const msg = REASON_MESSAGES[selectedReason];
    if (msg) {
      setDetail(t(msg));
    }
    setStep('confirm');
  };

  const handleCancel = async () => {
    try {
      await cancelSubscription({ reason: selectedReason, detail });
      setStep('success');
      onSuccess();
    } catch {
      setErrorMsg(t('plan.changeError'));
      setStep('error');
    }
  };

  const renderContent = () => {
    switch (step) {
      case 'reason':
        return (
          <div className="space-y-4">
            <h2 className="text-lg font-semibold text-gray-900">{t('cancel.title')}</h2>
            <p className="text-sm text-gray-600">{t('cancel.reason')}</p>
            <div className="space-y-2">
              {REASONS.map((r) => (
                <label
                  key={r.id}
                  className={`flex cursor-pointer items-center gap-3 rounded-lg border p-3 transition-colors ${
                    selectedReason === r.id
                      ? 'border-accent-500 bg-accent-50'
                      : 'border-gray-200 hover:bg-gray-50'
                  }`}
                >
                  <input
                    type="radio"
                    name="cancel-reason"
                    value={r.id}
                    checked={selectedReason === r.id}
                    onChange={(e) => setSelectedReason(e.target.value)}
                    className="h-4 w-4 text-accent-600"
                  />
                  <span className="text-sm text-gray-900">{t(r.key)}</span>
                </label>
              ))}
              {selectedReason === 'other' && (
                <textarea
                  value={detail}
                  onChange={(e) => setDetail(e.target.value)}
                  placeholder="Tell us more..."
                  rows={3}
                  className="mt-2 w-full rounded-md border border-gray-300 p-2 text-sm"
                />
              )}
            </div>
            <div className="flex justify-end gap-3 pt-4">
              <button
                type="button"
                onClick={onClose}
                className="rounded-md px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              >
                {t('cta.cancel')}
              </button>
              <button
                type="button"
                onClick={handleContinue}
                disabled={!selectedReason}
                className="rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800 disabled:opacity-50"
              >
                {t('onboard.continue')}
              </button>
            </div>
          </div>
        );

      case 'confirm':
        return (
          <div className="space-y-4">
            <h2 className="text-lg font-semibold text-gray-900">{t('cancel.confirmTitle')}</h2>
            <p className="text-sm text-gray-600">{t('cancel.confirmBody').replace('{date}', 'the end of your billing period')}</p>
            <div className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
              {detail}
            </div>
            <label className="flex items-start gap-3">
              <input
                type="checkbox"
                checked={checked}
                onChange={(e) => setChecked(e.target.checked)}
                className="mt-0.5 h-4 w-4 rounded border-gray-300 text-accent-600"
              />
              <span className="text-sm text-gray-700">{t('cancel.confirmCheckbox')}</span>
            </label>
            <div className="flex justify-end gap-3 pt-4">
              <button
                type="button"
                onClick={() => setStep('reason')}
                className="rounded-md px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              >
                {t('cta.cancel')}
              </button>
              <button
                type="button"
                onClick={() => void handleCancel()}
                disabled={!checked}
                className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-red-700 disabled:opacity-50"
              >
                {t('cancel.confirmButton')}
              </button>
            </div>
          </div>
        );

      case 'success':
        return (
          <div className="space-y-4 text-center">
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-gray-100">
              <svg className="h-6 w-6 text-gray-600" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-gray-900">{t('cancel.success')}</h2>
            <p className="text-sm text-gray-500">{t('dash.reactivateHint')}</p>
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
              <svg className="h-6 w-6 text-red-600" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-gray-900">{t('plan.changeError')}</h2>
            {errorMsg && <p className="text-sm text-red-600">{errorMsg}</p>}
            <div className="flex justify-center gap-3">
              <button
                type="button"
                onClick={() => setStep('reason')}
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
