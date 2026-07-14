import { useState, useCallback } from 'react';

interface Step {
  title: string;
  body: string;
}

const STEPS: Step[] = [
  {
    title: 'Welcome to your dashboard',
    body: 'This is your command center. Use the sidebar to navigate between sections.',
  },
  {
    title: 'Feed',
    body: 'Your personalised opportunity feed. Browse, save, and apply to roles that match your profile.',
  },
  {
    title: 'Matches',
    body: 'See opportunities that are the best fit based on your preferences and CV.',
  },
  {
    title: 'Saved & Applications',
    body: 'Keep track of opportunities you have saved or applied to.',
  },
  {
    title: 'Preferences & Settings',
    body: 'Update your match criteria, billing, and account settings any time.',
  },
];

const STORAGE_KEY = 'stawi.tour_completed';

function isTourCompleted(): boolean {
  try {
    return localStorage.getItem(STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

function markTourCompleted() {
  try {
    localStorage.setItem(STORAGE_KEY, '1');
  } catch {
    /* private mode */
  }
}

export function GuidedTour({ onDismiss }: { onDismiss: () => void }) {
  const [step, setStep] = useState(0);
  const isLast = step === STEPS.length - 1;

  const handleNext = useCallback(() => {
    if (isLast) {
      markTourCompleted();
      onDismiss();
    } else {
      setStep((s) => s + 1);
    }
  }, [isLast, onDismiss]);

  const handleSkip = useCallback(() => {
    markTourCompleted();
    onDismiss();
  }, [onDismiss]);

  const s = STEPS[step]!;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="mx-4 w-full max-w-sm rounded-xl bg-white p-6 shadow-xl dark:bg-navy-900">
        <div className="mb-6 flex items-center justify-between">
          <span className="text-xs font-medium text-gray-400 dark:text-gray-500">
            {step + 1} of {STEPS.length}
          </span>
          <button
            type="button"
            onClick={handleSkip}
            className="text-xs font-medium text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
          >
            Skip tour
          </button>
        </div>
        <h3 className="text-lg font-semibold text-gray-900 dark:text-white">{s.title}</h3>
        <p className="mt-2 text-sm text-gray-600 dark:text-gray-300">{s.body}</p>
        <div className="mt-6 flex items-center justify-between">
          <div className="flex gap-1.5">
            {STEPS.map((_, i) => (
              <span
                key={i}
                className={`block h-1.5 w-1.5 rounded-full ${
                  i === step ? 'bg-accent-500' : 'bg-gray-300 dark:bg-navy-600'
                }`}
              />
            ))}
          </div>
          <button
            type="button"
            onClick={handleNext}
            className="min-h-[44px] rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-navy-800"
          >
            {isLast ? 'Done' : 'Next'}
          </button>
        </div>
      </div>
    </div>
  );
}

export { isTourCompleted, markTourCompleted };
