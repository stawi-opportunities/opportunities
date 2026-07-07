import { useState } from 'react';
import { Button } from '@/components/ui/Button';
import { createCheckout } from '@/api/billing';
import { planById, type PlanId } from '@/utils/plans';

export function CompletePaymentPanel({ plan, status }: { plan: PlanId | null; status: string }) {
  const headline =
    status === 'past_due'
      ? "Your last payment didn't go through"
      : status === 'cancelled'
        ? 'Your subscription is cancelled'
        : plan
          ? `Finish setting up your ${planById(plan).name} plan`
          : 'Pick a plan to start matching';
  const body =
    status === 'past_due'
      ? 'Update your payment details to resume matching.'
      : status === 'cancelled'
        ? 'Re-activate any time to start receiving matches again.'
        : "We'll only run our matching engine on your CV once a plan is active. It takes two minutes.";
  return (
    <div className="rounded-lg border border-amber-300 bg-amber-50 p-6 dark:border-amber-700 dark:bg-amber-900/20">
      <p className="text-xs font-semibold uppercase tracking-wide text-amber-700 dark:text-amber-300">
        Action needed
      </p>
      <h2 className="mt-2 text-xl font-bold text-gray-900 dark:text-white">{headline}</h2>
      <p className="mt-1 text-sm text-gray-700 dark:text-gray-300">{body}</p>
      <div className="mt-4 flex flex-wrap gap-3">
        {plan && status !== 'cancelled' ? (
          <RetryCheckoutButton plan={plan} />
        ) : (
          <Button variant="primary" size="sm" onClick={() => (window.location.href = '/pricing/')}>
            Choose a plan
          </Button>
        )}
        <Button
          variant="secondary"
          size="sm"
          onClick={() => (window.location.href = '/onboarding/')}
        >
          Edit preferences
        </Button>
      </div>
    </div>
  );
}

function RetryCheckoutButton({ plan }: { plan: PlanId }) {
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const info = planById(plan);

  const go = async () => {
    setBusy(true);
    setErr(null);
    try {
      const res = await createCheckout({ plan_id: plan });
      if (res.status === 'redirect' && res.redirect_url) {
        window.location.href = res.redirect_url;
        return;
      }
      if (res.status === 'pending' && res.prompt_id) {
        window.location.href = `/dashboard/?billing=pending&prompt_id=${encodeURIComponent(res.prompt_id)}`;
        return;
      }
      if (res.status === 'paid') {
        window.location.href = '/dashboard/?billing=success';
        return;
      }
      throw new Error(res.error || 'Checkout did not complete.');
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Checkout failed. Please try again.');
      setBusy(false);
    }
  };

  return (
    <div>
      <Button variant="primary" size="sm" type="button" onClick={() => void go()} disabled={busy}>
        {busy ? 'Opening payment…' : `Pay $${info.price}/mo`}
      </Button>
      {err && <p className="mt-2 text-xs text-red-700 dark:text-red-400">{err}</p>}
    </div>
  );
}
