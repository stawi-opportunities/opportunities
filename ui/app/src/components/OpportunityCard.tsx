import type { FeedItem } from '@/api/candidates';
import { useI18n } from '@/i18n/I18nProvider';
import type { StringKey } from '@/i18n/strings';

export interface OpportunitySnapshot {
  title: string;
  company?: string;
  location?: string;
  posted_at?: string;
  salary_min?: number;
  salary_max?: number;
  currency?: string;
  kind?: string;
}

interface Props {
  item: FeedItem;
  snapshot: OpportunitySnapshot | null;
  onStar: (opportunityId: string) => void;
  onUnstar: (opportunityId: string) => void;
  onApply: (opportunityId: string) => void;
  isPending?: boolean;
}

const STATUS_KEYS: Record<string, StringKey> = {
  applied: 'status.applied',
  responded: 'status.responded',
  interview: 'status.interview',
  offer: 'status.offer',
  rejected: 'status.rejected',
  hired: 'status.hired',
};

export function OpportunityCard({ item, snapshot, onStar, onUnstar, onApply, isPending }: Props) {
  const { t } = useI18n();
  const title = snapshot?.title ?? item.opportunity_id;
  const company = snapshot?.company ?? '';
  const location = snapshot?.location ?? '';
  const isNew = snapshot?.posted_at
    ? Date.now() - new Date(snapshot.posted_at).getTime() < 24 * 60 * 60 * 1000
    : false;
  const isMatched = (item.score ?? 0) > 0;

  return (
    <li
      className={`flex flex-col gap-3 rounded-lg border bg-white p-4 sm:flex-row sm:items-start sm:gap-4 dark:bg-navy-900 ${
        isMatched
          ? 'border-l-4 border-l-emerald-500 border-gray-200 dark:border-navy-700'
          : 'border-gray-200 dark:border-navy-700'
      }`}
    >
      <div className="flex-1">
        <div className="flex items-start justify-between gap-2">
          <div>
            <h3 className="text-base font-semibold text-gray-900 dark:text-white">
              {title}
              {isNew && (
                <span className="ml-2 inline-flex items-center rounded-full bg-emerald-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
                  {t('card.new')}
                </span>
              )}
            </h3>
            {(company || location) && (
              <p className="mt-0.5 text-sm text-gray-600 dark:text-gray-300">
                {company}
                {company && location && ' · '}
                {location}
              </p>
            )}
          </div>
          {isMatched && (
            <span
              className="shrink-0 rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
              title="Match score"
            >
              {Math.round(item.score! * 100)}
              {t('card.match')}
            </span>
          )}
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          {item.application ? (
            <span className="inline-flex items-center rounded-full bg-blue-50 px-2.5 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-300">
              {STATUS_KEYS[item.application.status]
                ? t(STATUS_KEYS[item.application.status]!)
                : item.application.status}
            </span>
          ) : (
            <button
              type="button"
              onClick={() => onApply(item.opportunity_id)}
              disabled={isPending}
              className="rounded-md bg-navy-900 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-navy-800 disabled:opacity-50"
            >
              {t('cta.apply')}
            </button>
          )}
          {item.starred ? (
            <button
              type="button"
              onClick={() => onUnstar(item.opportunity_id)}
              aria-label="Remove from saved"
              disabled={isPending}
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-amber-700 transition-colors hover:bg-amber-50 disabled:opacity-50 dark:border-navy-700 dark:bg-navy-900 dark:text-amber-300 dark:hover:bg-amber-900/20"
            >
              ★ {t('cta.saved')}
            </button>
          ) : (
            <button
              type="button"
              onClick={() => onStar(item.opportunity_id)}
              aria-label="Save opportunity"
              disabled={isPending}
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 disabled:opacity-50 dark:border-navy-700 dark:bg-navy-900 dark:text-gray-300 dark:hover:bg-navy-800"
            >
              ☆ {t('cta.save')}
            </button>
          )}
        </div>
      </div>
    </li>
  );
}
