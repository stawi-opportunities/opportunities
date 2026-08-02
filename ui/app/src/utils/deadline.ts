/**
 * Expiry-first date system for opportunities.
 *
 * Primary date = application / closing / expiry deadline.
 * Posted-at is secondary (fallback when no deadline exists).
 * Crawl / first-seen timestamps are never surfaced to users.
 */

import { timeAgo } from './format';

export type DeadlineUrgency = 'expired' | 'today' | 'urgent' | 'soon' | 'later' | 'none';

export type DateSource = 'deadline' | 'posted' | 'none';

const MS_DAY = 86_400_000;

/** Resolve the action deadline from the usual wire fields. */
export function resolveDeadlineIso(fields: {
  deadline?: string | null;
  expires_at?: string | null;
  attributes?: Record<string, unknown> | null;
}): string | null {
  if (fields.deadline && !Number.isNaN(Date.parse(fields.deadline))) {
    return fields.deadline;
  }
  if (fields.expires_at && !Number.isNaN(Date.parse(fields.expires_at))) {
    return fields.expires_at;
  }
  const attrExpiry = fields.attributes?.expiry;
  if (typeof attrExpiry === 'string' && !Number.isNaN(Date.parse(attrExpiry))) {
    return attrExpiry;
  }
  return null;
}

/** Whole calendar days from `now` until the deadline (negative = past). */
export function daysUntil(iso: string, now = Date.now()): number | null {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return null;
  // Compare by UTC calendar day so "today" is stable across timezones
  // for date-only deadlines that land at midnight UTC.
  const startOfToday = startOfUtcDay(now);
  const startOfThen = startOfUtcDay(then);
  return Math.round((startOfThen - startOfToday) / MS_DAY);
}

function startOfUtcDay(ms: number): number {
  const d = new Date(ms);
  return Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
}

export function urgencyForDeadline(
  iso: string | null | undefined,
  now = Date.now()
): DeadlineUrgency {
  if (!iso) return 'none';
  const days = daysUntil(iso, now);
  if (days === null) return 'none';
  if (days < 0) return 'expired';
  if (days === 0) return 'today';
  if (days <= 3) return 'urgent';
  if (days <= 7) return 'soon';
  return 'later';
}

/** Kind-aware verb key fragment. */
export function deadlineVerbKey(
  kind?: string | null
): 'deadline.closes' | 'deadline.expires' | 'deadline.applyBy' {
  switch ((kind ?? '').toLowerCase()) {
    case 'tender':
      return 'deadline.closes';
    case 'deal':
      return 'deadline.expires';
    default:
      return 'deadline.applyBy';
  }
}

export function formatAbsoluteDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  });
}

/** Relative remaining time: "today", "tomorrow", "in 3 days", "closed". */
export function formatDeadlineRelative(iso: string, now = Date.now()): string {
  const days = daysUntil(iso, now);
  if (days === null) return '';
  if (days < 0) {
    const ago = Math.abs(days);
    if (ago === 1) return '1 day ago';
    if (ago < 30) return `${ago} days ago`;
    return formatAbsoluteDate(iso);
  }
  if (days === 0) return 'today';
  if (days === 1) return 'tomorrow';
  if (days < 14) return `in ${days} days`;
  if (days < 60) {
    const weeks = Math.round(days / 7);
    return weeks === 1 ? 'in 1 week' : `in ${weeks} weeks`;
  }
  return formatAbsoluteDate(iso);
}

/**
 * Compact list-row label: "Today", "1d left", "3d left", "Closed", "Mar 15".
 */
export function formatDeadlineShort(iso: string, now = Date.now()): string {
  const days = daysUntil(iso, now);
  if (days === null) return '';
  if (days < 0) return 'Closed';
  if (days === 0) return 'Today';
  if (days === 1) return '1d left';
  if (days <= 14) return `${days}d left`;
  return formatAbsoluteDate(iso);
}

/** Tailwind tone classes for urgency. */
export function urgencyToneClass(urgency: DeadlineUrgency): string {
  switch (urgency) {
    case 'expired':
      return 'text-gray-400 dark:text-gray-500';
    case 'today':
      return 'text-red-700 font-semibold dark:text-red-400';
    case 'urgent':
      return 'text-orange-700 font-semibold dark:text-orange-400';
    case 'soon':
      return 'text-orange-600 dark:text-orange-400';
    case 'later':
      return 'text-gray-700 dark:text-gray-300';
    default:
      return 'text-gray-500 dark:text-gray-400';
  }
}

export interface PrimaryDate {
  iso: string | null;
  source: DateSource;
  urgency: DeadlineUrgency;
  /** Full line for detail headers, e.g. "Apply by Mar 15 · in 3 days". */
  label: string;
  /** Compact for list right-rail, e.g. "3d left". */
  shortLabel: string;
  /** Accessible title with absolute date. */
  title: string;
  toneClass: string;
  verb: string;
  absolute: string;
  relative: string;
}

export interface PrimaryDateLabels {
  applyBy?: string;
  closes?: string;
  expires?: string;
  posted?: string;
  closed?: string;
}

const DEFAULT_LABELS: Required<PrimaryDateLabels> = {
  applyBy: 'Apply by',
  closes: 'Closes',
  expires: 'Expires',
  posted: 'Posted',
  closed: 'Closed',
};

/**
 * Pick and format the primary user-facing date.
 * Prefers deadline/expiry; falls back to posted_at; never crawl time.
 */
export function primaryDate(opts: {
  deadline?: string | null;
  expires_at?: string | null;
  posted_at?: string | null;
  kind?: string | null;
  attributes?: Record<string, unknown> | null;
  now?: number;
  labels?: PrimaryDateLabels;
}): PrimaryDate {
  const now = opts.now ?? Date.now();
  const L = { ...DEFAULT_LABELS, ...opts.labels };
  const dl = resolveDeadlineIso({
    deadline: opts.deadline,
    expires_at: opts.expires_at,
    attributes: opts.attributes,
  });

  if (dl) {
    const urgency = urgencyForDeadline(dl, now);
    const absolute = formatAbsoluteDate(dl);
    const relative = formatDeadlineRelative(dl, now);
    const verb =
      deadlineVerbKey(opts.kind) === 'deadline.closes'
        ? L.closes
        : deadlineVerbKey(opts.kind) === 'deadline.expires'
          ? L.expires
          : L.applyBy;
    const shortLabel =
      urgency === 'expired' ? L.closed : formatDeadlineShort(dl, now);
    const label =
      urgency === 'expired'
        ? `${verb} ${absolute} · ${L.closed.toLowerCase()}`
        : `${verb} ${absolute} · ${relative}`;
    return {
      iso: dl,
      source: 'deadline',
      urgency,
      label,
      shortLabel,
      title: `${verb} ${absolute}`,
      toneClass: urgencyToneClass(urgency),
      verb,
      absolute,
      relative,
    };
  }

  if (opts.posted_at && !Number.isNaN(Date.parse(opts.posted_at))) {
    const ago = timeAgo(opts.posted_at);
    const absolute = formatAbsoluteDate(opts.posted_at);
    return {
      iso: opts.posted_at,
      source: 'posted',
      urgency: 'none',
      label: `${L.posted} ${ago}`,
      shortLabel: ago,
      title: `${L.posted} ${absolute}`,
      toneClass: urgencyToneClass('none'),
      verb: L.posted,
      absolute,
      relative: ago,
    };
  }

  return {
    iso: null,
    source: 'none',
    urgency: 'none',
    label: '',
    shortLabel: '',
    title: '',
    toneClass: urgencyToneClass('none'),
    verb: '',
    absolute: '',
    relative: '',
  };
}
