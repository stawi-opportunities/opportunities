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
}

const STATUS_KEYS: Record<string, StringKey> = {
  applied: 'status.applied',
  responded: 'status.responded',
  interview: 'status.interview',
  offer: 'status.offer',
  rejected: 'status.rejected',
  hired: 'status.hired',
};

export function OpportunityCard({ item, snapshot, onStar, onUnstar, onApply }: Props) {
  const { t } = useI18n();
  const title = snapshot?.title ?? t('common.loading');
  const company = snapshot?.company ?? '';
  const location = snapshot?.location ?? '';

  return (
    <li className="flex flex-col gap-3 rounded-lg border border-gray-200 bg-white p-4 sm:flex-row sm:items-start sm:gap-4">
      <div className="flex-1">
        <div className="flex items-start justify-between gap-2">
          <div>
            <h3 className="text-base font-semibold text-gray-900">{title}</h3>
            {(company || location) && (
              <p className="mt-0.5 text-sm text-gray-600">
                {company}
                {company && location && ' · '}
                {location}
              </p>
            )}
          </div>
          {typeof item.score === 'number' && item.score > 0 && (
            <span
              className="shrink-0 rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs font-medium text-emerald-700"
              title="Match score"
            >
              {Math.round(item.score * 100)}
              {t('card.match')}
            </span>
          )}
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          {item.application ? (
            <span className="inline-flex items-center rounded-full bg-blue-50 px-2.5 py-0.5 text-xs font-medium text-blue-700">
              {STATUS_KEYS[item.application.status]
                ? t(STATUS_KEYS[item.application.status]!)
                : item.application.status}
            </span>
          ) : (
            <button
              type="button"
              onClick={() => onApply(item.opportunity_id)}
              className="rounded-md bg-navy-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-navy-800"
            >
              {t('cta.apply')}
            </button>
          )}
          {item.starred ? (
            <button
              type="button"
              onClick={() => onUnstar(item.opportunity_id)}
              aria-label="Remove from saved"
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-amber-700 hover:bg-amber-50"
            >
              ★ {t('cta.saved')}
            </button>
          ) : (
            <button
              type="button"
              onClick={() => onStar(item.opportunity_id)}
              aria-label="Save opportunity"
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              ☆ {t('cta.save')}
            </button>
          )}
        </div>
      </div>
    </li>
  );
}
