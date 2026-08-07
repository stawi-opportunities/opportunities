import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { pollCheckoutStatus } from '@/api/billing';
import { fetchMeSubscription } from '@/api/profile';
import { CelebrationOverlay } from '@/components/dashboard/CelebrationOverlay';
import { useI18n } from '@/i18n/I18nProvider';
import { QUERY_KEYS } from '@/constants/queryKeys';
import { clearPendingPrompt, PENDING_PROMPT_KEY, startCheckoutAndNavigate } from '@/utils/checkout';
import { Button } from '@/components/ui/Button';
import { useSubscription } from '@/hooks/useSubscription';
import { isPaidSubscriptionStatus } from '@/hooks/useSubscriptionGate';
import { normalizePlan } from '@/utils/plans';

type Phase = 'idle' | 'polling' | 'paid' | 'failed' | 'success';

const ONBOARDING_PATH = '/onboarding/';

/**
 * Checkout recovery — waits for billing to confirm payment AND for matching
 * entitlement (/me/subscription status=active) before treating the user as
 * subscribed. Product dashboard must not open on ?billing=success alone.
 *
 *   ?billing=success[&prompt_id=]  — return from pay.stawi.org
 *   ?billing=pending&prompt_id=    — rare: pay URL not ready at create
 *   ?billing=failed                — pay failed; retry or onboarding
 */
export function PendingCheckoutPoller() {
  const qc = useQueryClient();
  const { t } = useI18n();
  const subQ = useSubscription();
  const [phase, setPhase] = useState<Phase>('idle');
  const [error, setError] = useState<string | null>(null);
  const [retryBusy, setRetryBusy] = useState(false);

  // If entitlement already active, nothing to confirm.
  useEffect(() => {
    if (isPaidSubscriptionStatus(subQ.data?.status) && phase === 'idle') {
      // Clean leftover billing query if present.
      const u = new URL(window.location.href);
      if (u.searchParams.has('billing')) {
        u.searchParams.delete('billing');
        u.searchParams.delete('prompt_id');
        u.searchParams.delete('session');
        window.history.replaceState(null, '', u.pathname + u.hash + (u.search || ''));
      }
    }
  }, [subQ.data?.status, phase]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const billing = params.get('billing');
    const urlPromptId = params.get('prompt_id');
    const stashed = readStash();
    const promptId = urlPromptId ?? stashed;

    // ── Return from hosted checkout ─────────────────────────────────
    if (billing === 'success') {
      clearPendingPrompt();
      setPhase('polling');
      let cancelled = false;
      void (async () => {
        // 1) Drive checkout activation if webhook is slow.
        if (promptId) {
          const deadline = Date.now() + 45_000;
          while (!cancelled && Date.now() < deadline) {
            try {
              const res = await pollCheckoutStatus(promptId);
              if (res.status === 'failed') {
                if (!cancelled) {
                  setError(res.error || "Payment didn't complete.");
                  setPhase('failed');
                }
                return;
              }
              if (res.status === 'paid') break;
            } catch {
              /* keep trying */
            }
            await sleep(2_000);
          }
        }
        // 2) Wait until matching entitlement is active (billing confirmed).
        const subOk = await waitForActiveSubscription(qc, () => cancelled);
        if (cancelled) return;
        if (!subOk) {
          setError(
            'Payment may have succeeded, but subscription is not active yet. Try again or finish setup.'
          );
          setPhase('failed');
          return;
        }
        setPhase('success');
        // Clean URL so refresh doesn't re-enter confirm loop.
        const u = new URL(window.location.href);
        if (u.searchParams.has('billing') || u.searchParams.has('prompt_id')) {
          u.searchParams.delete('billing');
          u.searchParams.delete('prompt_id');
          u.searchParams.delete('session');
          window.history.replaceState(null, '', u.pathname + u.hash + (u.search || ''));
        }
      })();
      return () => {
        cancelled = true;
      };
    }

    // ── Failed (no fresh prompt) ────────────────────────────────────
    if (billing === 'failed' && !urlPromptId) {
      setPhase('failed');
      setError("Payment didn't complete.");
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
          const subOk = await waitForActiveSubscription(qc, () => cancelled);
          if (cancelled) return;
          if (!subOk) {
            setError(
              'Payment received, but subscription is not active yet. Contact support if this persists.'
            );
            setPhase('failed');
            return;
          }
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
      window.location.assign('/onboarding/');
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
    // Entitlement is active — full page reload picks up allowed dashboard.
    return (
      <CelebrationOverlay
        t={t}
        onDismiss={() => {
          window.location.replace('/dashboard/');
        }}
      />
    );
  }
  if (phase === 'failed') {
    return (
      <div className="mt-4 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800 dark:border-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
        <p>{error ?? "Payment didn't complete."}</p>
        <div className="mt-3 flex flex-wrap gap-2">
          <Button
            variant="primary"
            size="sm"
            type="button"
            disabled={retryBusy}
            onClick={() => void retry()}
          >
            {retryBusy ? 'Opening payment…' : 'Retry payment'}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            type="button"
            onClick={() => window.location.replace(ONBOARDING_PATH)}
          >
            Back to setup
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
      Confirming payment with billing — the dashboard opens only when your subscription is active.
    </div>
  );
}

/** Poll checkout activation + /me/subscription until status is active. */
async function waitForActiveSubscription(
  qc: ReturnType<typeof useQueryClient>,
  isCancelled: () => boolean,
  maxMs = 60_000
): Promise<boolean> {
  const deadline = Date.now() + maxMs;
  while (!isCancelled() && Date.now() < deadline) {
    try {
      await qc.invalidateQueries({ queryKey: QUERY_KEYS.SUBSCRIPTION });
      const sub = await fetchMeSubscription();
      if (isPaidSubscriptionStatus(sub.status)) return true;
    } catch {
      /* keep trying */
    }
    await sleep(2_000);
  }
  try {
    const sub = await fetchMeSubscription();
    return isPaidSubscriptionStatus(sub.status);
  } catch {
    return false;
  }
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
  window.history.replaceState(null, '', u.pathname + u.search + u.hash);
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}
