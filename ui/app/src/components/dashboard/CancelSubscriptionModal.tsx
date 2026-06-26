import { useEffect, useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { cancelSubscription } from '@/api/billing';
import { useFocusTrap } from '@/hooks/useFocusTrap';
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
  const dialogRef = useRef<HTMLDivElement | null>(null);

  useFocusTrap(dialogRef, true, onClose);

  useEffect(() => {
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = '';
    };
  }, []);

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
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
              {t('cancel.title')}
            </h2>
            <p className="text-sm text-gray-600 dark:text-gray-400">{t('cancel.reason')}</p>
            <div className="space-y-2">
              {REASONS.map((r) => (
                <label
                  key={r.id}
                  className={`flex cursor-pointer items-center gap-3 rounded-lg border p-3 transition-colors ${
                    selectedReason === r.id
                      ? 'border-accent-500 bg-accent-50 dark:border-accent-600 dark:bg-accent-900/20'
                      : 'border-gray-200 hover:bg-gray-50 dark:border-navy-700 dark:hover:bg-navy-800'
                  }`}
                >
                  <input
                    type="radio"
                    name="cancel-reason"
                    value={r.id}
                    checked={selectedReason === r.id}
                    onChange={(e) => setSelectedReason(e.target.value)}
                    className="h-4 w-4 text-accent-600 dark:text-accent-400"
                  />
                  <span className="text-sm text-gray-900 dark:text-white">{t(r.key)}</span>
                </label>
              ))}
              {selectedReason === 'other' && (
                <textarea
                  value={detail}
                  onChange={(e) => setDetail(e.target.value)}
                  placeholder="Tell us more..."
                  rows={3}
                  className="input-field mt-2"
                />
              )}
            </div>
            <div className="flex justify-end gap-3 pt-4">
              <Button variant="secondary" size="md" type="button" onClick={onClose}>
                {t('cta.cancel')}
              </Button>
              <Button
                variant="primary"
                size="md"
                type="button"
                onClick={handleContinue}
                disabled={!selectedReason}
              >
                {t('onboard.continue')}
              </Button>
            </div>
          </div>
        );

      case 'confirm':
        return (
          <div className="space-y-4">
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
              {t('cancel.confirmTitle')}
            </h2>
            <p className="text-sm text-gray-600 dark:text-gray-400">
              {t('cancel.confirmBody').replace('{date}', 'the end of your billing period')}
            </p>
            <div className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
              {detail}
            </div>
            <label className="flex items-start gap-3">
              <input
                type="checkbox"
                checked={checked}
                onChange={(e) => setChecked(e.target.checked)}
                className="mt-0.5 h-4 w-4 rounded border-gray-300 text-accent-600 dark:border-navy-500 dark:bg-navy-800 dark:text-accent-400"
              />
              <span className="text-sm text-gray-700 dark:text-gray-300">
                {t('cancel.confirmCheckbox')}
              </span>
            </label>
            <div className="flex justify-end gap-3 pt-4">
              <Button variant="secondary" size="md" type="button" onClick={() => setStep('reason')}>
                {t('cta.cancel')}
              </Button>
              <Button
                variant="danger"
                size="md"
                type="button"
                onClick={() => void handleCancel()}
                disabled={!checked}
              >
                {t('cancel.confirmButton')}
              </Button>
            </div>
          </div>
        );

      case 'success':
        return (
          <div className="space-y-4 text-center">
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-gray-100 dark:bg-navy-700">
              <svg
                className="h-6 w-6 text-gray-600 dark:text-gray-300"
                fill="none"
                viewBox="0 0 24 24"
                strokeWidth={2}
                stroke="currentColor"
              >
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold text-gray-900 dark:text-white">
              {t('cancel.success')}
            </h2>
            <p className="text-sm text-gray-500 dark:text-gray-400">{t('dash.reactivateHint')}</p>
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
              <Button variant="primary" size="md" type="button" onClick={() => setStep('reason')}>
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
