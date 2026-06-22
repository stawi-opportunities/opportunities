import type { UsageEntry } from '@/api/billing';
import type { StringKey } from '@/i18n/strings';

function Bar({
  value,
  max,
  color,
}: {
  value: number;
  max: number;
  color: string;
}) {
  const height = max > 0 ? `${Math.round((value / max) * 100)}%` : '0%';
  return (
    <div
      className={`w-full rounded-t ${color}`}
      style={{ height }}
      title={`${value}`}
    />
  );
}

export function UsageChart({
  history,
  t,
}: {
  history: UsageEntry[];
  t: (k: StringKey, fallback?: string) => string;
}) {
  if (!history.length) {
    return (
      <div className="rounded-lg border border-gray-200 bg-gray-50 p-6 text-center text-sm text-gray-500">
        {t('usage.noData')}
      </div>
    );
  }

  const maxVal = Math.max(...history.flatMap((h) => [h.delivered, h.queued]), 1);

  const barW = `${Math.max(20, Math.min(40, 280 / history.length))}px`;

  return (
    <div>
      <div className="flex items-end justify-center gap-1" style={{ height: '160px' }}>
        {history.map((entry) => (
          <div key={entry.week} className="flex flex-col items-center gap-0.5" style={{ width: barW }}>
            <div className="flex h-full w-full items-end justify-center gap-px">
              <div
                className="flex w-1/2 flex-col-reverse"
                style={{ height: '100%' }}
              >
                <Bar value={entry.delivered} max={maxVal} color="bg-accent-500" />
              </div>
              <div
                className="flex w-1/2 flex-col-reverse"
                style={{ height: '100%' }}
              >
                <Bar value={entry.queued} max={maxVal} color="bg-blue-300" />
              </div>
            </div>
            <span className="text-[10px] text-gray-400">{entry.week.slice(-5)}</span>
          </div>
        ))}
      </div>
      <div className="mt-3 flex items-center justify-center gap-4 text-xs text-gray-500">
        <span className="flex items-center gap-1.5">
          <span className="inline-block h-2.5 w-2.5 rounded-sm bg-accent-500" />
          {t('usage.delivered')}
        </span>
        <span className="flex items-center gap-1.5">
          <span className="inline-block h-2.5 w-2.5 rounded-sm bg-blue-300" />
          {t('usage.queued')}
        </span>
      </div>
    </div>
  );
}
