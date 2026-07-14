/**
 * Live checklist of required matching fields — shows exact values captured
 * so far and what is still missing for plan unlock.
 */

import type { OnboardingChatFields, OnboardingChatFieldStatus } from '@/api/candidates';
import { FIELD_LABELS } from './mapFields';

const REQUIRED_ORDER = [
  'target_job_title',
  'capabilities',
  'job_types',
  'salary_expectation',
  'preferred_countries',
  'experience_level',
] as const;

function valueForKey(key: string, fields: OnboardingChatFields): string {
  switch (key) {
    case 'target_job_title':
      return fields.target_job_title?.trim() || '';
    case 'capabilities':
      if (fields.extra_info && fields.extra_info.length >= 120) return 'CV provided';
      return '';
    case 'job_types':
      return fields.job_types?.length ? fields.job_types.join(', ') : '';
    case 'salary_expectation': {
      if (fields.salary_min == null && fields.salary_max == null) return '';
      const cur = fields.currency || 'USD';
      const lo = fields.salary_min ?? fields.salary_max;
      const hi = fields.salary_max ?? fields.salary_min;
      return lo === hi ? `${cur} ${lo}` : `${cur} ${lo}–${hi}`;
    }
    case 'preferred_countries':
      return fields.preferred_countries?.length
        ? fields.preferred_countries.join(', ')
        : fields.country?.trim() || '';
    case 'experience_level':
      return fields.experience_level?.trim() || '';
    default:
      return '';
  }
}

export function RequiredDataPanel({
  fields,
  missing,
  fieldStatus,
  compact = false,
}: {
  fields: OnboardingChatFields;
  missing: string[];
  fieldStatus?: Record<string, OnboardingChatFieldStatus>;
  compact?: boolean;
}) {
  const missSet = new Set(missing);
  const done = REQUIRED_ORDER.filter((k) => {
    if (fieldStatus?.[k]) return fieldStatus[k]!.ok;
    return !missSet.has(k) && Boolean(valueForKey(k, fields));
  }).length;

  return (
    <section
      className={`rounded-2xl border border-gray-200/80 bg-white/90 shadow-sm dark:border-navy-700 dark:bg-navy-900/80 ${
        compact ? 'p-3' : 'p-4'
      }`}
      aria-label="Required matching data"
    >
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <h3 className="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
          Required for matching
        </h3>
        <span className="text-xs font-medium text-gray-600 dark:text-gray-300">
          {done}/{REQUIRED_ORDER.length}
        </span>
      </div>
      <ul className="space-y-1.5">
        {REQUIRED_ORDER.map((key) => {
          const status = fieldStatus?.[key];
          const ok = status ? status.ok : !missSet.has(key) && Boolean(valueForKey(key, fields));
          const value = status?.value || valueForKey(key, fields);
          const reason = status?.reason;
          return (
            <li key={key} className="flex items-start gap-2 text-sm">
              <span
                className={`mt-0.5 inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[10px] font-bold ${
                  ok
                    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/50 dark:text-emerald-300'
                    : 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200'
                }`}
                aria-hidden
              >
                {ok ? '✓' : '!'}
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-x-2">
                  <span className="font-medium text-gray-800 dark:text-gray-100">
                    {FIELD_LABELS[key] ?? key}
                  </span>
                  {ok && value ? (
                    <span className="truncate text-gray-600 dark:text-gray-300" title={value}>
                      {value}
                    </span>
                  ) : (
                    <span className="text-xs text-amber-800/90 dark:text-amber-300/90">
                      {reason || 'Still needed'}
                    </span>
                  )}
                </div>
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
