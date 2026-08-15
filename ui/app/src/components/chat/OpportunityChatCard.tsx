/**
 * Reusable job/opportunity widget for chat transcripts.
 * Renders title, company·location, and a clickable apply / sign-in link.
 */

import { useAuth } from '@/providers/AuthProvider';

export type OpportunityChatCardData = {
  title: string;
  subtitle?: string;
  /** Same-origin listing path, e.g. /jobs/foo/ */
  href: string;
  apply_url?: string;
  opportunity_id?: string;
  slug?: string;
};

export function opportunityCardFromSnap(snap: {
  title: string;
  issuing_entity?: string;
  slug: string;
  kind?: string;
  apply_url?: string | null;
  remote?: boolean;
  anchor_location?: { city?: string; region?: string; country?: string } | null;
}): OpportunityChatCardData {
  const locParts = [
    snap.anchor_location?.city,
    snap.anchor_location?.region,
    snap.anchor_location?.country,
  ].filter(Boolean);
  const location = locParts.length ? locParts.join(', ') : snap.remote ? 'Remote' : '';
  const subtitle = [snap.issuing_entity, location].filter(Boolean).join(' · ');
  const kind = (snap.kind || 'job').toLowerCase();
  const prefix =
    kind === 'scholarship' || kind === 'scholarships'
      ? 'scholarships'
      : kind === 'tender' || kind === 'tenders'
        ? 'tenders'
        : kind === 'deal' || kind === 'deals'
          ? 'deals'
          : kind === 'funding'
            ? 'funding'
            : 'jobs';
  return {
    title: snap.title,
    subtitle,
    href: `/${prefix}/${encodeURIComponent(snap.slug)}/`,
    apply_url: snap.apply_url ?? undefined,
    slug: snap.slug,
  };
}

type Props = {
  card: OpportunityChatCardData;
  className?: string;
};

export function OpportunityChatCard({ card, className = '' }: Props) {
  const { hasSession, login } = useAuth();
  const applyURL = card.apply_url?.trim();
  const href =
    hasSession && applyURL
      ? applyURL
      : applyURL
        ? `${card.href}${card.href.includes('?') ? '&' : '?'}apply=1`
        : card.href;

  return (
    <a
      href={href}
      target={hasSession && applyURL ? '_blank' : undefined}
      rel={hasSession && applyURL ? 'noopener noreferrer' : undefined}
      onClick={(e) => {
        if (hasSession || !applyURL) return;
        e.preventDefault();
        try {
          const url = new URL(window.location.href);
          url.searchParams.set('apply', '1');
          window.history.replaceState({}, '', url.pathname + url.search + url.hash);
        } catch {
          /* ignore */
        }
        void login();
      }}
      className={`block rounded-xl border border-stone-200 bg-white px-3.5 py-3 shadow-sm transition hover:border-blue-300 hover:shadow dark:border-navy-700 dark:bg-navy-950 ${className}`}
    >
      <p className="text-sm font-semibold text-stone-900 dark:text-stone-100">{card.title}</p>
      {card.subtitle ? (
        <p className="mt-0.5 text-xs text-stone-500 dark:text-stone-400">{card.subtitle}</p>
      ) : null}
      {applyURL ? (
        <p className="mt-1.5 text-xs font-medium text-blue-600">Apply →</p>
      ) : (
        <p className="mt-1.5 text-xs font-medium text-blue-600">View listing →</p>
      )}
    </a>
  );
}
