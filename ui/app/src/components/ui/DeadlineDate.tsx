/**
 * Shared expiry-first date chip used on list rows, cards, and detail headers.
 *
 * Primary date is always deadline/expiry when present. Posted-at is secondary
 * only (shown muted under the full variant when a deadline exists).
 */

import { primaryDate, type PrimaryDateLabels } from '@/utils/deadline';
import { timeAgo } from '@/utils/format';
import { useI18n } from '@/i18n/I18nProvider';
import clsx from 'clsx';

export interface DeadlineDateProps {
  deadline?: string | null;
  expires_at?: string | null;
  posted_at?: string | null;
  kind?: string | null;
  attributes?: Record<string, unknown> | null;
  /** 'full' = "Apply by Mar 15 · in 3 days"; 'short' = "3d left". */
  variant?: 'full' | 'short';
  className?: string;
  /** When true, hide entirely if no date (default true). */
  hideIfEmpty?: boolean;
  as?: 'time' | 'span';
  /**
   * With variant=full and a deadline, also render a muted "Posted …" line.
   * Default true for full, false for short.
   */
  showPostedSecondary?: boolean;
}

export function DeadlineDate({
  deadline,
  expires_at,
  posted_at,
  kind,
  attributes,
  variant = 'short',
  className,
  hideIfEmpty = true,
  as = 'time',
  showPostedSecondary,
}: DeadlineDateProps) {
  const { t } = useI18n();
  const labels: PrimaryDateLabels = {
    applyBy: t('deadline.applyBy'),
    closes: t('deadline.closes'),
    expires: t('deadline.expires'),
    posted: t('deadline.posted'),
    closed: t('deadline.closed'),
  };
  const d = primaryDate({
    deadline,
    expires_at,
    posted_at,
    kind,
    attributes,
    labels,
  });

  if (!d.iso || !d.label) {
    if (hideIfEmpty) return null;
    return null;
  }

  const text = variant === 'full' ? d.label : d.shortLabel;
  const Tag = as;
  const datetime = d.iso.slice(0, 10);
  const secondary =
    (showPostedSecondary ?? variant === 'full') &&
    d.source === 'deadline' &&
    posted_at &&
    !Number.isNaN(Date.parse(posted_at));

  const primaryEl = (
    <Tag
      {...(as === 'time' ? { dateTime: datetime } : {})}
      title={d.title}
      className={clsx(
        'whitespace-nowrap tabular-nums',
        variant === 'short' ? 'text-xs' : 'text-sm',
        d.toneClass,
        d.urgency === 'expired' && 'line-through decoration-gray-400',
        !secondary && className
      )}
      data-date-source={d.source}
      data-urgency={d.urgency}
    >
      {text}
    </Tag>
  );

  if (!secondary) return primaryEl;

  return (
    <span className={clsx('inline-flex flex-col items-start gap-0.5', className)}>
      {primaryEl}
      <span className="text-xs text-gray-400 dark:text-gray-500">
        {t('deadline.posted')} {timeAgo(posted_at)}
      </span>
    </span>
  );
}
