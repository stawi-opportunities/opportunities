import type { StringKey } from '@/i18n/strings';

const STEP_LABEL_KEYS: StringKey[] = [
  'onboard.aboutYou',
  'onboard.yourPreferences',
  'onboard.choosePlan',
];

interface Props {
  step: 1 | 2 | 3;
  t: (k: StringKey, fallback?: string) => string;
}

export function StepProgress({ step, t }: Props) {
  return (
    <div>
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
        {t('onboard.step')} {step} {t('onboard.of')} 3 · {t(STEP_LABEL_KEYS[step - 1]!)}
      </p>
      <nav className="mt-4" aria-label="Onboarding steps">
        <ol className="flex items-center gap-0">
          {STEP_LABEL_KEYS.map((key, i) => {
            const n = (i + 1) as 1 | 2 | 3;
            const done = step > n;
            const active = step === n;
            return (
              <li key={key} className="flex flex-1 items-center">
                <div className="flex flex-col items-center gap-1.5">
                  {done ? (
                    <div className="flex h-8 w-8 items-center justify-center rounded-full bg-accent-500">
                      <svg className="h-4 w-4 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M5 13l4 4L19 7" />
                      </svg>
                    </div>
                  ) : (
                    <div
                      className={`flex h-8 w-8 items-center justify-center rounded-full border-2 text-sm font-semibold transition-colors ${
                        active
                          ? 'border-accent-500 bg-accent-50 text-accent-700'
                          : 'border-gray-200 bg-white text-gray-400'
                      }`}
                    >
                      {n}
                    </div>
                  )}
                  <span
                    className={`text-xs leading-tight ${
                      active ? 'font-medium text-gray-900' : 'text-gray-500'
                    }`}
                  >
                    {t(key)}
                  </span>
                </div>
                {i < 2 && (
                  <div
                    className={`mx-2 mt-[-1.75rem] h-0.5 flex-1 self-center transition-colors ${
                      done ? 'bg-accent-500' : 'bg-gray-200'
                    }`}
                    aria-hidden
                  />
                )}
              </li>
            );
          })}
        </ol>
      </nav>
    </div>
  );
}
