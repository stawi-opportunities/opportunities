import { planById, type PlanId } from '@/utils/plans';
import { Panel } from './Panel';

export function MatchesPanel({
  plan,
  queued,
  delivered,
  subQueryError,
  onUpgrade,
}: {
  plan: PlanId;
  queued: number | null;
  delivered: number | null;
  subQueryError: boolean;
  onUpgrade?: () => void;
}) {
  if (plan === 'managed') {
    return (
      <Panel title="Matches">
        <p className="text-sm text-gray-600 dark:text-gray-400">
          Your agent hand-picks roles that pass their screen before they reach you. Expect curated
          matches in your inbox and a weekly summary on your 1:1 call.
        </p>
      </Panel>
    );
  }
  const planInfo = planById(plan);
  const cap = planInfo.matchesPerWeek ?? 0;
  const progressPct = cap > 0 ? Math.min(100, Math.round(((delivered ?? 0) / cap) * 100)) : 0;

  if (subQueryError || queued === null || delivered === null) {
    return (
      <Panel title="Matches">
        <p className="text-sm text-amber-700 dark:text-amber-300">
          We couldn't load your latest match numbers. Refresh in a few seconds — if this keeps
          happening, drop us a line at{' '}
          <a href="mailto:jobs@stawi.org" className="underline">
            jobs@stawi.org
          </a>
          .
        </p>
      </Panel>
    );
  }

  return (
    <Panel title="Matches">
      <div className="grid grid-cols-2 gap-4">
        <div>
          <p className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            Delivered this week
          </p>
          <div className="mt-2 flex items-baseline gap-1">
            <span className="text-2xl font-bold text-gray-900 dark:text-white">{delivered}</span>
            <span className="text-sm text-gray-500 dark:text-gray-400">/ {cap}</span>
          </div>
          <div className="mt-2 h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-navy-700">
            <div
              className="h-full rounded-full bg-accent-500 transition-all duration-700 ease-out"
              style={{ width: `${progressPct}%` }}
            />
          </div>
        </div>
        <div>
          <p className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
            In your queue
          </p>
          <p className="mt-2 text-2xl font-bold text-gray-900 dark:text-white">{queued}</p>
        </div>
      </div>
      {plan === 'starter' && (
        <div className="mt-4 rounded-md border border-gray-200 bg-gray-50 p-3 text-sm text-gray-700 dark:border-navy-600 dark:bg-navy-800 dark:text-gray-300">
          Want 5× the matches and priority placement in the queue?{' '}
          {onUpgrade ? (
            <button
              type="button"
              onClick={onUpgrade}
              className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
            >
              Upgrade to Pro →
            </button>
          ) : (
            <a
              href="/pricing/"
              className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-300"
            >
              Upgrade to Pro →
            </a>
          )}
        </div>
      )}
      {delivered === 0 && (
        <p className="mt-4 text-sm text-gray-500 dark:text-gray-400">
          Your first matches will arrive within 24 hours of payment.
        </p>
      )}
    </Panel>
  );
}
