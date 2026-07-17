import { useState } from 'react';
import { PLANS, type PlanId } from '@/utils/plans';
import type { StringKey } from '@/i18n/strings';

interface Props {
  value: PlanId;
  onChange: (id: PlanId) => void;
  t: (k: StringKey, fallback?: string) => string;
}

export function PlanSelector({ value, onChange, t }: Props) {
  const [expanded, setExpanded] = useState<PlanId | null>(null);

  return (
    <div className="grid gap-4 sm:grid-cols-2" role="radiogroup" aria-label="Plan">
      {PLANS.map((p) => {
        const on = value === p.id;
        const showAll = expanded === p.id;
        return (
          <div
            key={p.id}
            className={`relative flex flex-col rounded-lg border-2 p-5 transition-colors ${
              on
                ? 'border-accent-500 bg-accent-50 shadow-sm dark:bg-accent-900/20'
                : 'border-gray-200 bg-white hover:border-gray-300 dark:border-navy-600 dark:bg-navy-800 dark:hover:border-navy-500'
            }`}
          >
            {p.highlight && (
              <span className="absolute -top-2.5 right-3 rounded-full bg-amber-200 px-2.5 py-0.5 text-xs font-semibold text-amber-800 dark:bg-amber-900/40 dark:text-amber-300">
                Most popular
              </span>
            )}

            <button
              type="button"
              role="radio"
              aria-checked={on}
              onClick={() => onChange(p.id)}
              className="flex flex-col items-start gap-1 text-left"
            >
              <span className="text-lg font-bold text-gray-900 dark:text-white">{p.name}</span>
              <span className="text-xl font-bold text-gray-900 dark:text-white">
                ${p.price}
                <span className="text-sm font-normal text-gray-500 dark:text-gray-400">
                  {t('dash.perMonth')}
                </span>
              </span>
              <span className="mt-1 text-sm text-gray-600 dark:text-gray-300">{p.tagline}</span>
              {p.matchesPerWeek !== null && (
                <span className="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {t('onboard.matchesPerWeek').replace('{count}', String(p.matchesPerWeek))}
                </span>
              )}
              {p.matchesPerWeek === null && (
                <span className="mt-1 text-xs font-medium text-accent-700">
                  Unlimited discovery
                </span>
              )}
              {p.meta.autoApply && (
                <span className="mt-1 text-xs font-medium text-accent-700">
                  {t('onboard.includesAgent')}
                </span>
              )}
            </button>

            <div className="mt-3 flex-1">
              <button
                type="button"
                onClick={() => setExpanded(showAll ? null : p.id)}
                className="text-xs font-medium text-accent-600 hover:text-accent-700"
              >
                {showAll
                  ? `${t('onboard.showLess')} ↑`
                  : `${p.features.length} ${t('onboard.features')} →`}
              </button>
              {showAll && (
                <ul className="mt-2 space-y-1">
                  {p.features.map((f) => (
                    <li
                      key={f}
                      className="flex items-start gap-1.5 text-xs text-gray-500 dark:text-gray-400"
                    >
                      <svg
                        className="mt-0.5 h-3 w-3 shrink-0 text-emerald-500"
                        fill="none"
                        stroke="currentColor"
                        viewBox="0 0 24 24"
                      >
                        <path
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          strokeWidth="2"
                          d="M5 13l4 4L19 7"
                        />
                      </svg>
                      {f}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
