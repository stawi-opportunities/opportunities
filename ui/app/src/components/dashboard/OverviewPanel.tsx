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
    <div className="rounded-xl border border-gray-200 bg-white p-5 dark:border-navy-700 dark:bg-navy-900">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-base font-semibold text-gray-900 dark:text-white">Overview</h2>
        {(queued != null || delivered != null) && (
          <p className="text-sm text-gray-500 tabular-nums">
            {queued ?? 0} queued · {delivered ?? 0} this week
          </p>
        )}
      </div>
      <div className="mt-4 flex flex-wrap gap-2">
        <button
          type="button"
          onClick={onGoMatches}
          className="rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white hover:bg-navy-800"
        >
          {hasMatches ? 'Matches' : 'Find matches'}
        </button>
        <button
          type="button"
          onClick={onGoTools}
          className="rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-800 hover:bg-gray-50 dark:border-navy-600 dark:bg-navy-900 dark:text-gray-200"
        >
          Tools
        </button>
        {freeProof && (
          <button
            type="button"
            onClick={onGoBilling}
            className="rounded-md px-4 py-2 text-sm font-medium text-gray-600 hover:text-gray-900 dark:text-gray-300"
          >
            Upgrade
          </button>
        )}
      </div>
    </div>
  );
}
