import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { pollCheckoutStatus } from '@/api/billing';
import { CelebrationOverlay } from '@/components/dashboard/CelebrationOverlay';
import { useI18n } from '@/i18n/I18nProvider';
import { QUERY_KEYS } from '@/constants/queryKeys';
import { clearPendingPrompt, PENDING_PROMPT_KEY, startCheckoutAndNavigate } from '@/utils/checkout';
import { Button } from '@/components/ui/Button';
import { useSubscription } from '@/hooks/useSubscription';
import { normalizePlan } from '@/utils/plans';

type Phase = 'idle' | 'polling' | 'paid' | 'failed' | 'success';

/**
 * Dashboard checkout recovery — only three URL modes:
 *
 *   ?billing=success[&prompt_id=]  — return from Flutterwave (step 4→5)
 *   ?billing=pending&prompt_id=    — rare: URL not ready at create time
 *   ?billing=failed                — pay failed; offer retry
 *
 * On success with prompt_id, polls status once-loop to drive activation
 * (webhook may already have run). Then celebrates + refreshes subscription.
 */
export function PendingCheckoutPoller() {
  const qc = useQueryClient();
  const { t } = useI18n();
  const subQ = useSubscription();
  const [phase, setPhase] = useState<Phase>('idle');
  const [error, setError] = useState<string | null>(null);
  const [retryBusy, setRetryBusy] = useState(false);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const billing = params.get('billing');
    const urlPromptId = params.get('prompt_id');
    const stashed = readStash();
    const promptId = urlPromptId ?? stashed;

    // ── Return from Flutterwave ─────────────────────────────────────
    if (billing === 'success') {
      clearPendingPrompt();
      setPhase('success');
      let cancelled = false;
      void (async () => {
        // Drive activation if webhook is slow (needs prompt_id).
        if (promptId) {
          const deadline = Date.now() + 45_000;
          while (!cancelled && Date.now() < deadline) {
            try {
              const res = await pollCheckoutStatus(promptId);
              if (res.status === 'paid' || res.status === 'failed') break;
            } catch {
              /* keep trying */
            }
            await sleep(2_000);
          }
        }
        if (!cancelled) {
          await qc.invalidateQueries({ queryKey: QUERY_KEYS.SUBSCRIPTION });
          // Clean URL so a refresh doesn't re-enter the success poller loop.
          const u = new URL(window.location.href);
          if (u.searchParams.has('billing') || u.searchParams.has('prompt_id')) {
            u.searchParams.delete('billing');
            u.searchParams.delete('prompt_id');
            u.searchParams.delete('session');
            window.history.replaceState(null, '', u.pathname + u.hash + (u.search || ''));
          }
        }
      })();
      return () => {
        cancelled = true;
      };
    }

    // ── Failed (no fresh prompt) ────────────────────────────────────
    if (billing === 'failed' && !urlPromptId) {
      return;
    }

    // ── Pending recovery (or stash after refresh) ───────────────────
    if (!promptId) return;
    if (urlPromptId) stash(urlPromptId);

    let cancelled = false;
    setPhase('polling');
    const start = Date.now();
    const MAX_MS = 3 * 60 * 1000;

    const tick = async () => {
      if (cancelled) return;
      try {
        const res = await pollCheckoutStatus(promptId);
        if (cancelled) return;
        if (res.redirect_url && billing !== 'success') {
          stash(promptId);
          window.location.assign(res.redirect_url);
          return;
        }
        if (res.status === 'paid') {
          clearPendingPrompt();
          await qc.invalidateQueries({ queryKey: QUERY_KEYS.SUBSCRIPTION });
          setPhase('paid');
          replaceBillingQuery('success', promptId);
          return;
        }
        if (res.status === 'failed') {
          clearPendingPrompt();
          setError(res.error || "Payment didn't complete.");
          setPhase('failed');
          replaceBillingQuery('failed');
          return;
        }
      } catch {
        /* transient */
      }
      if (Date.now() - start > MAX_MS) {
        setError("We're still waiting for your payment provider. Try again below.");
        setPhase('failed');
        clearPendingPrompt();
        return;
      }
      setTimeout(tick, 4_000);
    };
    void tick();
    return () => {
      cancelled = true;
    };
  }, [qc]);

  const retry = async () => {
    const plan = normalizePlan(subQ.data?.plan ?? null);
    if (!plan) {
      window.location.assign('/pricing/');
      return;
    }
    setRetryBusy(true);
    setError(null);
    try {
      await startCheckoutAndNavigate({ plan_id: plan });
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not open checkout.');
      setRetryBusy(false);
    }
  };

  if (phase === 'idle') return null;
  if (phase === 'paid' || phase === 'success') {
    return <CelebrationOverlay t={t} onDismiss={() => setPhase('idle')} />;
  }
  if (phase === 'failed') {
    return (
      <div className="mt-4 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
        <p>{error ?? "Payment didn't complete."} You can retry below.</p>
        <div className="mt-3">
          <Button
            variant="primary"
            size="sm"
            type="button"
            disabled={retryBusy}
            onClick={() => void retry()}
          >
            {retryBusy ? 'Opening payment…' : 'Retry payment'}
          </Button>
        </div>
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
      Confirming your payment — this usually takes under a minute.
    </div>
  );
}

function readStash(): string | null {
  try {
    return localStorage.getItem(PENDING_PROMPT_KEY);
  } catch {
    return null;
  }
}

function stash(id: string) {
  try {
    localStorage.setItem(PENDING_PROMPT_KEY, id);
  } catch {
    /* private mode */
  }
}

function replaceBillingQuery(billing: string, promptId?: string) {
  const u = new URL(window.location.href);
  u.searchParams.set('billing', billing);
  if (promptId) u.searchParams.set('prompt_id', promptId);
  else u.searchParams.delete('prompt_id');
  window.history.replaceState(null, '', u.toString());
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}
