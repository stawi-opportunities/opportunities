import type { FeedItem } from '@/api/candidates';
import { useI18n } from '@/i18n/I18nProvider';
import type { StringKey } from '@/i18n/strings';
import { useAuth } from '@/providers/AuthProvider';
import { useSubscription } from '@/hooks/useSubscription';
import { Icon } from '@/components/ui/Icon';
import { Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { DeadlineDate } from '@/components/ui/DeadlineDate';
import { getTypeMeta } from '@/constants/opportunityTypes';
import { urgencyForDeadline } from '@/utils/deadline';
import { scoreToPercent, whyMatched } from '@/utils/matchScore';

const KIND_PATH: Record<string, string> = {
  job: 'jobs',
  scholarship: 'scholarships',
  tender: 'tenders',
  deal: 'deals',
  funding: 'funding',
};

function detailUrl(snapshot: OpportunitySnapshot): string {
  const path = KIND_PATH[snapshot.kind ?? ''] ?? snapshot.kind;
  return snapshot.slug && path ? `/${path}/${snapshot.slug}/` : '';
}

export interface OpportunitySnapshot {
  title: string;
  company?: string;
  location?: string;
  posted_at?: string;
  deadline?: string;
  salary_min?: number;
  salary_max?: number;
  currency?: string;
  kind?: string;
  id?: string;
  slug?: string;
  has_how_to_apply?: boolean;
  apply_url?: string;
}

interface Props {
  item: FeedItem;
  snapshot: OpportunitySnapshot | null;
  onStar: (opportunityId: string) => void;
  onUnstar: (opportunityId: string) => void;
  onApply: (opportunityId: string) => void;
  onDismiss?: (matchId: string, opportunityId: string) => void;
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

export function OpportunityCard({
  item,
  snapshot,
  onStar,
  onUnstar,
  onApply,
  onDismiss,
  isPending,
}: Props) {
  const { t } = useI18n();
  const { hasSession } = useAuth();
  const sub = useSubscription();
  const st = sub.data?.status;
  const active = hasSession && (st === 'active' || st === 'past_due' || st === 'trial');
  const title = snapshot?.title ?? item.opportunity_id;
  const company = snapshot?.company ?? '';
  const location = snapshot?.location ?? '';
  const isNew = snapshot?.posted_at
    ? Date.now() - new Date(snapshot.posted_at).getTime() < 24 * 60 * 60 * 1000
    : false;
  const closingUrgency = urgencyForDeadline(snapshot?.deadline);
  const isClosingSoon =
    closingUrgency === 'today' || closingUrgency === 'urgent' || closingUrgency === 'soon';
  const matchPct = scoreToPercent(item.score);
  const isMatched = matchPct != null && matchPct > 0;
  const why = whyMatched(item.score);
  const canDismiss = Boolean(item.match_id && onDismiss);
  const typeMeta = snapshot?.kind ? getTypeMeta(snapshot.kind) : null;

  return (
    <li
      className={`ds-list-item flex flex-col gap-3 sm:flex-row sm:items-start sm:gap-4 ${
        isMatched ? 'border-l-[3px] border-l-accent-500' : ''
      }`}
    >
      <div className="min-w-0 flex-1">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-1.5">
              {isNew && <Badge variant="success">{t('card.new')}</Badge>}
              {isClosingSoon && <Badge variant="warning">{t('deadline.closingSoon')}</Badge>}
              {typeMeta && (
                <Badge variant="neutral" className="gap-1 font-normal">
                  <Icon name={typeMeta.iconName} size={10} />
                  {t(typeMeta.labelKey)}
                </Badge>
              )}
            </div>
            <h3 className="mt-1.5 text-base font-semibold leading-snug tracking-tight text-main">
              <span className="break-words">{title}</span>
            </h3>
            {(company || location) && (
              <p className="mt-0.5 text-sm text-secondary">
                {company}
                {company && location && ' · '}
                {location}
              </p>
            )}
            <DeadlineDate
              deadline={snapshot?.deadline}
              posted_at={snapshot?.posted_at}
              kind={snapshot?.kind}
              variant="full"
              className="mt-1 block text-sm text-secondary"
            />
            {why && (
              <p
                className="mt-1.5 text-xs leading-relaxed text-accent-700 dark:text-accent-300"
                data-testid="why-matched"
              >
                {why}
              </p>
            )}
          </div>
          {isMatched && matchPct != null && (
            <span
              className="shrink-0 rounded-lg bg-accent-500/10 px-2.5 py-1 text-sm font-semibold tabular-nums text-accent-700 dark:text-accent-300"
              title="Match score (CV + preferences fit)"
            >
              {matchPct}
              {t('card.match')}
            </span>
          )}
        </div>

        <div className="mt-3 flex flex-wrap items-stretch gap-2 sm:items-center">
          {item.application ? (
            <Badge variant="info">
              {STATUS_KEYS[item.application.status]
                ? t(STATUS_KEYS[item.application.status]!)
                : item.application.status}
            </Badge>
          ) : (
            <>
              {active && snapshot?.has_how_to_apply && detailUrl(snapshot) ? (
                <Button as="a" href={detailUrl(snapshot)} size="md" className="flex-1 sm:flex-none">
                  {t('card.howToApply')}
                </Button>
              ) : (
                <Button
                  type="button"
                  size="md"
                  className="flex-1 sm:flex-none"
                  onClick={() => onApply(item.opportunity_id)}
                  disabled={isPending}
                >
                  {t('cta.apply')}
                </Button>
              )}
            </>
          )}
          {item.starred ? (
            <Button
              type="button"
              variant="secondary"
              size="md"
              onClick={() => onUnstar(item.opportunity_id)}
              aria-label="Remove from saved"
              disabled={isPending}
            >
              ★ {t('cta.saved')}
            </Button>
          ) : (
            <Button
              type="button"
              variant="secondary"
              size="md"
              onClick={() => onStar(item.opportunity_id)}
              aria-label="Save opportunity"
              disabled={isPending}
            >
              ☆ {t('cta.save')}
            </Button>
          )}
          {canDismiss && (
            <Button
              type="button"
              variant="ghost"
              size="md"
              onClick={() => onDismiss!(item.match_id!, item.opportunity_id)}
              aria-label="Dismiss match"
              disabled={isPending}
              title="Hide this match — improves future digests"
            >
              Dismiss
            </Button>
          )}
        </div>
      </div>
    </li>
  );
}
