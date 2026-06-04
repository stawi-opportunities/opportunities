import { type PlanId } from '@/utils/plans';
import { Panel } from './Panel';

export function ApplicationsPanel({ plan }: { plan: PlanId }) {
  return (
    <Panel title="Applications">
      <p className="text-sm text-gray-600">
        {plan === 'pro'
          ? 'Pro includes cover-letter drafts for every match. Open a match to generate one.'
          : "Every listing links to the employer's own application page — we'll track the ones you start from here once the employer-callback integrations ship."}
      </p>
    </Panel>
  );
}
