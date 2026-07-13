import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import { useI18n } from '@/i18n/I18nProvider';

function computeCompleteness(
  data:
    | { current_title?: string; preferred_countries?: string; languages?: string }
    | null
    | undefined
): number {
  const checks = [!!data?.current_title, !!data?.preferred_countries, !!data?.languages];
  const done = checks.filter(Boolean).length;
  return Math.round((done / checks.length) * 100);
}

export function ProfileCompleteness() {
  const { t } = useI18n();
  const { data, isLoading } = useCandidateProfile();
  if (isLoading) return null;
  const score = computeCompleteness(data);
  const missing = [];
  if (!data?.current_title) missing.push(t('dash.compTitle'));
  if (!data?.preferred_countries) missing.push(t('dash.compCountries'));
  if (!data?.languages) missing.push(t('dash.compLanguages'));

  return (
    <div className="rounded-xl border-0 bg-white p-4 shadow-sm ring-1 ring-gray-200 dark:bg-navy-900 dark:ring-navy-700">
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
      {missing.length > 0 && (
        <ul className="mt-3 space-y-1.5">
          {missing.map((label) => (
            <li
              key={label}
              className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400"
            >
              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-amber-400" />
              {label}
            </li>
          ))}
        </ul>
      )}
      {score < 100 && (
        <a
          href="/dashboard/#settings"
          className="mt-3 inline-block text-xs font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
        >
          {t('dash.compLink')}
        </a>
      )}
    </div>
  );
}
