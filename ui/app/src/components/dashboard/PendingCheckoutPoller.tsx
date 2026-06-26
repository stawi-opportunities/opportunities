import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { pollCheckoutStatus } from '@/api/billing';
import { CelebrationOverlay } from '@/components/dashboard/CelebrationOverlay';
import { useI18n } from '@/i18n/I18nProvider';
import { QUERY_KEYS } from '@/constants/queryKeys';

const PENDING_PROMPT_KEY = 'stawi.billing.pending_prompt_id';

/**
 * Recovers a mid-flight checkout. When createCheckout returns
 * status:"pending" (M-PESA STK push, Polar async session), the
 * onboarding/dashboard navigates here with
 * `?billing=pending&prompt_id=…`. Without polling, the user is
 * stranded — the page sits with no feedback until they manually
 * refresh hours later. This component:
 *
 *   1. On mount, reads `prompt_id` from the URL OR from localStorage
 *      (a refresh-resilient stash so closing the tab doesn't strand
 *      the user).
 *   2. Long-polls /billing/checkout/status every 4s, up to ~3 min.
 *   3. On `paid` → clears the stash, invalidates the subscription
 *      query, swaps `?billing` for `?billing=success`.
 *   4. On `failed` → clears the stash, swaps to `?billing=failed`,
 *      surfaces the error in CompletePaymentPanel via URL params.
 */
export function PendingCheckoutPoller() {
  const qc = useQueryClient();
  const { t } = useI18n();
  const [state, setState] = useState<'idle' | 'polling' | 'paid' | 'failed'>('idle');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const urlPromptId = params.get('prompt_id');
    const stashed = (() => {
      try {
        return localStorage.getItem(PENDING_PROMPT_KEY);
      } catch {
        return null;
      }
    })();
    const promptId = urlPromptId ?? stashed;
    if (!promptId) return;

    if (urlPromptId && urlPromptId !== stashed) {
      try {
        localStorage.setItem(PENDING_PROMPT_KEY, urlPromptId);
      } catch {
        /* private mode */
      }
    }

    let cancelled = false;
    setState('polling');
    const start = Date.now();
    const MAX_MS = 3 * 60 * 1000;
    const INTERVAL_MS = 4_000;

    const tick = async () => {
      if (cancelled) return;
      try {
        const res = await pollCheckoutStatus(promptId);
        if (cancelled) return;
        if (res.status === 'paid') {
          try {
            localStorage.removeItem(PENDING_PROMPT_KEY);
          } catch {
            /* ignore */
          }
          await qc.invalidateQueries({ queryKey: QUERY_KEYS.SUBSCRIPTION });
          setState('paid');
          const u = new URL(window.location.href);
          u.searchParams.delete('prompt_id');
          u.searchParams.set('billing', 'success');
          window.history.replaceState(null, '', u.toString());
          return;
        }
        if (res.status === 'failed') {
          try {
            localStorage.removeItem(PENDING_PROMPT_KEY);
          } catch {
            /* ignore */
          }
          setError(res.error || "Payment didn't complete.");
          setState('failed');
          const u = new URL(window.location.href);
          u.searchParams.delete('prompt_id');
          u.searchParams.set('billing', 'failed');
          window.history.replaceState(null, '', u.toString());
          return;
        }
      } catch {
        // Transient — keep polling until the budget expires.
      }
      if (Date.now() - start > MAX_MS) {
        setError("We're still waiting for your payment provider. Try again from below.");
        setState('failed');
        return;
      }
      setTimeout(tick, INTERVAL_MS);
    };
    void tick();
    return () => {
      cancelled = true;
    };
  }, [qc]);

  if (state === 'idle') return null;
  if (state === 'paid') {
    return <CelebrationOverlay t={t} onDismiss={() => setState('idle')} />;
  }
  if (state === 'failed') {
    return (
      <div className="mt-4 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
        {error ?? "Payment didn't complete."} You can retry below.
      </div>
    );
  }
  return (
    <div
      className="mt-4 flex items-center gap-3 rounded-md border border-blue-200 bg-blue-50 p-4 text-sm text-blue-800 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-300"
      role="status"
      aria-live="polite"
    >
      <div className="h-4 w-4 animate-spin rounded-full border-2 border-blue-600 border-t-transparent dark:border-blue-300 dark:border-t-transparent" />
      Waiting for your payment provider to confirm — this usually takes under a minute. You can
      leave this tab open; we'll update it automatically.
    </div>
  );
}
