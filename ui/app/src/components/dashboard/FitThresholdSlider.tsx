import {
  clampMinFitPercent,
  DEFAULT_MIN_FIT_PERCENT,
  MAX_MIN_FIT_PERCENT,
} from '@/utils/matchScore';

interface Props {
  value: number;
  onChange: (percent: number) => void;
  className?: string;
  /** Compact inline layout for dense toolbars. */
  compact?: boolean;
}

/**
 * Range control to tighten the match shortlist quality floor.
 * Floor is 70% (server generation quality); max 95% for aggressive triage.
 */
export function FitThresholdSlider({ value, onChange, className = '', compact }: Props) {
  const pct = clampMinFitPercent(value);

  if (compact) {
    return (
      <div className={`flex items-center gap-2 ${className}`}>
        <label
          htmlFor="fit-threshold"
          className="flex shrink-0 items-baseline gap-1.5 text-xs font-medium text-secondary"
        >
          <span>Min fit</span>
          <span className="tabular-nums font-semibold text-accent-700 dark:text-accent-400">
            {pct}%+
          </span>
        </label>
        <input
          id="fit-threshold"
          type="range"
          min={DEFAULT_MIN_FIT_PERCENT}
          max={MAX_MIN_FIT_PERCENT}
          step={5}
          value={pct}
          onChange={(e) => onChange(clampMinFitPercent(Number(e.target.value)))}
          aria-label="Minimum match fit percentage"
          aria-valuemin={DEFAULT_MIN_FIT_PERCENT}
          aria-valuemax={MAX_MIN_FIT_PERCENT}
          aria-valuenow={pct}
          aria-valuetext={`${pct} percent match or higher`}
          className="h-2 w-28 cursor-pointer accent-accent-600 sm:w-36"
          title="Raise to show only stronger matches"
        />
      </div>
    );
  }

  return (
    <div
      className={`rounded-xl border border-muted bg-surface px-4 py-3 shadow-sm ${className}`}
      role="group"
      aria-labelledby="fit-threshold-heading"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <p id="fit-threshold-heading" className="text-sm font-semibold text-main">
            Minimum fit
          </p>
          <p className="mt-0.5 text-xs text-secondary">
            Drag right to tighten the shortlist (only stronger matches stay).
          </p>
        </div>
        <span className="shrink-0 rounded-lg bg-accent-500/10 px-2.5 py-1 text-sm font-semibold tabular-nums text-accent-700 dark:text-accent-300">
          {pct}%+
        </span>
      </div>
      <div className="mt-3 flex items-center gap-3">
        <span className="w-8 text-xs tabular-nums text-secondary">{DEFAULT_MIN_FIT_PERCENT}%</span>
        <input
          id="fit-threshold"
          type="range"
          min={DEFAULT_MIN_FIT_PERCENT}
          max={MAX_MIN_FIT_PERCENT}
          step={5}
          value={pct}
          onChange={(e) => onChange(clampMinFitPercent(Number(e.target.value)))}
          aria-label="Minimum match fit percentage"
          aria-valuemin={DEFAULT_MIN_FIT_PERCENT}
          aria-valuemax={MAX_MIN_FIT_PERCENT}
          aria-valuenow={pct}
          aria-valuetext={`${pct} percent match or higher`}
          className="h-2 w-full min-w-0 flex-1 cursor-pointer accent-accent-600"
          title="Raise to show only stronger matches"
        />
        <span className="w-8 text-right text-xs tabular-nums text-secondary">
          {MAX_MIN_FIT_PERCENT}%
        </span>
      </div>
    </div>
  );
}
