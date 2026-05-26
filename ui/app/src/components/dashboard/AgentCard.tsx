export function AgentCard({ agent }: { agent: { name: string; email: string } }) {
  return (
    <div className="rounded-lg border border-accent-200 bg-accent-50 p-6">
      <p className="text-xs font-semibold uppercase tracking-wide text-accent-700">
        Your agent
      </p>
      <h2 className="mt-2 text-xl font-bold text-gray-900">{agent.name}</h2>
      <p className="mt-1 text-sm text-gray-700">
        Your personal recruiter for the duration of your search. Reach out
        any time and expect a same-day response.
      </p>
      <div className="mt-4 flex flex-wrap gap-3">
        <a
          href={`mailto:${agent.email}`}
          className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-semibold text-white hover:bg-navy-800"
        >
          Email {agent.name.split(" ")[0]}
        </a>
        <a
          href="#schedule"
          className="inline-flex items-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          Schedule a 1:1
        </a>
      </div>
    </div>
  );
}
