import { useRef, useState, type FormEvent } from 'react';
import { authRuntime } from '@/auth/runtime';
import { useFocusTrap } from '@/hooks/useFocusTrap';
import { useI18n } from '@/i18n/I18nProvider';
import type { StringKey } from '@/i18n/strings';

const REASON_KEYS: { value: string; labelKey: StringKey }[] = [
  { value: 'scam', labelKey: 'flag.scam' },
  { value: 'expired', labelKey: 'flag.expired' },
  { value: 'duplicate', labelKey: 'flag.duplicate' },
  { value: 'spam', labelKey: 'flag.spam' },
  { value: 'other', labelKey: 'flag.other' },
];

const MAX_DESCRIPTION = 1000;

type SubmitState =
  | { kind: 'idle' }
  | { kind: 'submitting' }
  | { kind: 'ok' }
  | { kind: 'duplicate' }
  | { kind: 'unauthorized' }
  | { kind: 'error'; message: string };

export default function FlagModal({ slug }: { slug: string }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState<string>('scam');
  const [description, setDescription] = useState('');
  const [state, setState] = useState<SubmitState>({ kind: 'idle' });
  const dialogRef = useRef<HTMLDivElement | null>(null);
  useFocusTrap(dialogRef, open, () => setOpen(false));

  function reset(): void {
    setReason('scam');
    setDescription('');
    setState({ kind: 'idle' });
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (state.kind === 'submitting') return;
    setState({ kind: 'submitting' });
    try {
      await authRuntime().fetch(`/jobs/opportunities/${encodeURIComponent(slug)}/flag`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason, description: description.trim() }),
      });
      setState({ kind: 'ok' });
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      if (message.includes('409') || message.toLowerCase().includes('already_flagged')) {
        setState({ kind: 'duplicate' });
      } else if (message.includes('401') || message.toLowerCase().includes('unauthorized')) {
        setState({ kind: 'unauthorized' });
      } else {
        setState({ kind: 'error', message });
      }
    }
  }

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => {
          reset();
          setOpen(true);
        }}
        className="mt-6 inline-flex items-center gap-1 text-sm text-gray-500 hover:text-red-700"
      >
        <span aria-hidden>⚠</span>
        {t('flag.trigger')}
      </button>
    );
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="flag-modal-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) setOpen(false);
      }}
    >
      <div
        ref={dialogRef}
        className="w-full max-w-md max-h-[90vh] overflow-y-auto rounded-lg bg-white p-6 shadow-xl"
      >
        <h3 id="flag-modal-title" className="text-lg font-semibold text-gray-900">
          {t('flag.title')}
        </h3>

        {state.kind === 'ok' ? (
          <div className="mt-4 space-y-3 text-sm text-gray-700">
            <p>{t('flag.thankYou')}</p>
            <button
              type="button"
              onClick={() => setOpen(false)}
              className="rounded bg-navy-700 px-4 py-2 text-sm font-medium text-white"
            >
              {t('cta.close')}
            </button>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="mt-4 space-y-4">
            <div>
              <label htmlFor="flag-reason" className="block text-sm font-medium text-gray-700">
                {t('flag.reason')}
              </label>
              <select
                id="flag-reason"
                value={reason}
                onChange={(e) => setReason((e.target as HTMLSelectElement).value)}
                className="mt-1 block w-full rounded border border-gray-300 px-3 py-2 text-sm"
              >
                {REASON_KEYS.map((r) => (
                  <option key={r.value} value={r.value}>
                    {t(r.labelKey)}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label htmlFor="flag-description" className="block text-sm font-medium text-gray-700">
                {t('flag.details')}
              </label>
              <textarea
                id="flag-description"
                value={description}
                onChange={(e) => setDescription((e.target as HTMLTextAreaElement).value)}
                maxLength={MAX_DESCRIPTION}
                rows={4}
                className="mt-1 block w-full rounded border border-gray-300 px-3 py-2 text-sm"
                placeholder={t('flag.detailsPlaceholder')}
              />
              <p className="mt-1 text-right text-xs text-gray-500">
                {description.length}/{MAX_DESCRIPTION}
              </p>
            </div>

            {state.kind === 'duplicate' && (
              <p className="text-sm text-amber-700">{t('flag.alreadyFlagged')}</p>
            )}
            {state.kind === 'unauthorized' && (
              <p className="text-sm text-red-700">{t('flag.signInRequired')}</p>
            )}
            {state.kind === 'error' && (
              <p className="text-sm text-red-700">
                {t('error.submitFlag')} {state.message}
              </p>
            )}

            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="rounded border border-gray-300 px-4 py-2 text-sm text-gray-700"
              >
                {t('cta.cancel')}
              </button>
              <button
                type="submit"
                disabled={state.kind === 'submitting'}
                className="rounded bg-red-700 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
              >
                {state.kind === 'submitting' ? t('flag.submitting') : t('flag.submitButton')}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
