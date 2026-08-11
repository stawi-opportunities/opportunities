import {
  clampMinFitPercent,
  DEFAULT_MIN_FIT_PERCENT,
  MAX_MIN_FIT_PERCENT,
} from '@/utils/matchScore';

interface Props {
  value: number;
  onChange: (percent: number) => void;
  className?: string;
  /** Compact inline layout for the matches meta row. */
  compact?: boolean;
}

/**
 * Range control to tighten the match shortlist quality floor.
 * Floor is 70% (server generation quality); max 95% for aggressive triage.
 */
export function FitThresholdSlider({ value, onChange, className = '', compact }: Props) {
  const pct = clampMinFitPercent(value);

  return (
    <div
      className={`flex items-center gap-2 ${compact ? '' : 'w-full max-w-xs flex-col items-stretch sm:flex-row sm:items-center'} ${className}`}
    >
      <label
        htmlFor="fit-threshold"
        className="flex shrink-0 items-baseline gap-1.5 text-xs font-medium text-secondary"
      >
        <span className="sr-only sm:not-sr-only sm:inline">Min fit</span>
        <span
          className="tabular-nums font-semibold text-accent-700 dark:text-accent-400"
          aria-hidden="true"
        >
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
