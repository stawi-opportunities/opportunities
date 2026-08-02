interface Props {
  freeProof?: boolean;
  queued?: number;
  delivered?: number;
  onGoMatches?: () => void;
  onGoTools?: () => void;
  onGoBilling?: () => void;
}

export function OverviewPanel({
  freeProof = false,
  queued,
  delivered,
  onGoMatches,
  onGoTools,
  onGoBilling,
}: Props) {
  const hasMatches = (queued ?? 0) + (delivered ?? 0) > 0;

  return (
    <div className="rounded-xl border border-muted bg-surface p-5">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-base font-semibold text-main">Overview</h2>
        {(queued != null || delivered != null) && (
          <p className="text-sm text-secondary tabular-nums">
            {queued ?? 0} queued · {delivered ?? 0} this week
          </p>
        )}
      </div>
      <div className="mt-4 flex flex-wrap gap-2">
        <button
          type="button"
          onClick={onGoMatches}
          className="rounded-md bg-accent-500 px-4 py-2 text-sm font-medium text-navy-950 hover:bg-accent-400"
        >
          {hasMatches ? 'Matches' : 'Find matches'}
        </button>
        <button
          type="button"
          onClick={onGoTools}
          className="rounded-md border border-muted-strong bg-surface px-4 py-2 text-sm font-medium text-main hover:bg-surface-hover"
        >
          Tools
        </button>
        {freeProof && (
          <button
            type="button"
            onClick={onGoBilling}
            className="rounded-md px-4 py-2 text-sm font-medium text-secondary hover:text-main"
          >
            Upgrade
          </button>
        )}
      </div>
    </div>
  );
}
