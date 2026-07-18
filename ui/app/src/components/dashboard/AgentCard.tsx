import { Button } from '@/components/ui/Button';

/**
 * Optional support contact when a real agent assignment exists.
 * Never invent white-glove 1:1 coaching — only show assigned contact details.
 */
export function AgentCard({ agent }: { agent: { name: string; email: string } }) {
  const first = agent.name.split(' ')[0] || agent.name;
  return (
    <div className="rounded-lg border border-accent-200 bg-accent-50 p-6 dark:border-accent-800 dark:bg-accent-900/20">
      <p className="text-xs font-semibold uppercase tracking-wide text-accent-700 dark:text-accent-300">
        Priority support
      </p>
      <h2 className="mt-2 text-xl font-bold text-gray-900 dark:text-white">{agent.name}</h2>
      <p className="mt-1 text-sm text-gray-700 dark:text-gray-300">
        Your Managed plan includes a named support contact for match and account questions. We do not
        auto-apply or run interview coaching from this card.
      </p>
      <div className="mt-4 flex flex-wrap gap-3">
        <Button
          variant="primary"
          size="sm"
          onClick={() => {
            window.location.href = `mailto:${agent.email}`;
          }}
        >
          Email {first}
        </Button>
      </div>
    </div>
  );
}
