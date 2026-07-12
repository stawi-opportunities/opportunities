import { useCandidateProfile } from '@/hooks/useCandidateProfile';

interface CompletenessStep {
  key: string;
  label: string;
  done: boolean;
}

function computeCompleteness(
  data:
    | { current_title?: string; preferred_countries?: string; languages?: string }
    | null
    | undefined
): { score: number; steps: CompletenessStep[] } {
  const steps: CompletenessStep[] = [
    { key: 'title', label: 'Add target job title', done: !!data?.current_title },
    { key: 'countries', label: 'Set preferred countries', done: !!data?.preferred_countries },
    { key: 'languages', label: 'Add languages', done: !!data?.languages },
  ];
  const done = steps.filter((s) => s.done).length;
  return { score: Math.round((done / steps.length) * 100), steps };
}

export function ProfileCompleteness() {
  const { data, isLoading } = useCandidateProfile();
  if (isLoading) return null;
  const { score, steps } = computeCompleteness(data);
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-4 dark:border-navy-700 dark:bg-navy-900">
      <div className="flex items-center justify-between">
        <span className="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
          Profile completeness
        </span>
        <span className="text-sm font-semibold text-gray-900 dark:text-white">{score}%</span>
      </div>
      <div className="mt-2 h-2 w-full rounded-full bg-gray-100 dark:bg-navy-800">
        <div
          className={`h-2 rounded-full transition-all ${
            score === 100 ? 'bg-emerald-500' : score >= 50 ? 'bg-accent-500' : 'bg-amber-500'
          }`}
          style={{ width: `${score}%` }}
          role="progressbar"
          aria-valuenow={score}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-label="Profile completeness"
        />
      </div>
      {score < 100 && (
        <ul className="mt-3 space-y-1.5">
          {steps
            .filter((s) => !s.done)
            .map((s) => (
              <li
                key={s.key}
                className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400"
              >
                <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-amber-400" />
                {s.label}
              </li>
            ))}
        </ul>
      )}
      {score < 100 && (
        <a
          href="/dashboard/#settings"
          className="mt-3 inline-block text-xs font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
        >
          Complete profile →
        </a>
      )}
    </div>
  );
}
