import { lazy, Suspense, useEffect, useRef } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchSnapshot } from '@/api/snapshot';
import { pingApply, pingJobView } from '@/api/views';
import { categoryLabel, isoInPast, timeAgo } from '@/utils/format';
import { useI18n } from '@/i18n/I18nProvider';
import type { StringKey } from '@/i18n/strings';
import {
  setAnalyticsContext,
  trackApplyClick,
  trackJobView,
  trackJobViewEngaged,
} from '@/analytics/posthog';
import {
  isDeal,
  isFunding,
  isJob,
  isScholarship,
  isTender,
  type OpportunityKind,
  type OpportunitySnapshot,
} from '@/types/snapshot';
import { Icon } from '@/components/ui/Icon';
import { getTypeMeta } from '@/constants/opportunityTypes';
import HowToApplySection from '@/components/HowToApplySection';
import { OpportunitySideChat } from '@/components/OpportunitySideChat';
import { useAuth } from '@/providers/AuthProvider';

const JobBody = lazy(() => import('@/components/bodies/JobBody'));
const ScholarshipBody = lazy(() => import('@/components/bodies/ScholarshipBody'));
const TenderBody = lazy(() => import('@/components/bodies/TenderBody'));
const DealBody = lazy(() => import('@/components/bodies/DealBody'));
const FundingBody = lazy(() => import('@/components/bodies/FundingBody'));

export default function OpportunityDetail() {
  const { lang, t } = useI18n();
  const { hasSession } = useAuth();
  const autoApplyDone = useRef(false);

  const route = (() => {
    if (typeof window === 'undefined') return null;
    const m = window.location.pathname.match(/^\/([^/]+)\/([^/]+)\/?$/);
    if (!m) return null;
    return { prefix: m[1]!, slug: decodeURIComponent(m[2]!) };
  })();

  const q = useQuery({
    queryKey: ['snapshot', route?.prefix, route?.slug, lang],
    queryFn: () => fetchSnapshot(route!.slug),
    enabled: !!route,
    staleTime: 5 * 60_000,
  });

  const ldRef = useRef<HTMLScriptElement | null>(null);
  const mountedAtRef = useRef<number>(
    typeof performance !== 'undefined' ? performance.now() : Date.now()
  );

  // After login-to-apply (?apply=1), open the employer URL once for signed-in users.
  useEffect(() => {
    if (!hasSession || !q.data?.apply_url || autoApplyDone.current) return;
    if (typeof window === 'undefined') return;
    const params = new URLSearchParams(window.location.search);
    if (params.get('apply') !== '1') return;
    autoApplyDone.current = true;
    const snap = q.data;
    trackApplyClick({
      canonical_job_id: snap.id,
      slug: snap.slug,
      company: snap.issuing_entity,
      apply_url: snap.apply_url ?? '',
      dwell_ms: 0,
    });
    pingApply(snap.slug);
    window.open(snap.apply_url, '_blank', 'noopener,noreferrer');
    params.delete('apply');
    const clean =
      window.location.pathname +
      (params.toString() ? `?${params.toString()}` : '') +
      window.location.hash;
    window.history.replaceState({}, '', clean);
  }, [hasSession, q.data]);

  useEffect(() => {
    if (!q.data) return;
    const snap = q.data;
    setAnalyticsContext('canonical_job_id', snap.id);
    setAnalyticsContext('slug', snap.slug);
    setAnalyticsContext('kind', snap.kind);
    setAnalyticsContext('ui_language', lang);

    trackJobView({
      canonical_job_id: snap.id,
      slug: snap.slug,
      category: snap.categories?.[0],
      company: snap.issuing_entity,
      country: snap.anchor_location?.country,
      ui_language: lang,
      referrer: typeof document !== 'undefined' ? document.referrer : '',
    });

    void pingJobView(snap.slug);

    const engagedAt = setTimeout(() => {
      const dwell = Math.round(
        (typeof performance !== 'undefined' ? performance.now() : Date.now()) - mountedAtRef.current
      );
      const doc = typeof document !== 'undefined' ? document.documentElement : null;
      const scrollPct = doc
        ? Math.min(
            100,
            Math.round(((window.scrollY + window.innerHeight) / (doc.scrollHeight || 1)) * 100)
          )
        : 0;
      trackJobViewEngaged({
        canonical_job_id: snap.id,
        slug: snap.slug,
        dwell_ms: dwell,
        scroll_depth_pct: scrollPct,
      });
    }, 10_000);

    return () => clearTimeout(engagedAt);
  }, [q.data, lang]);

  useEffect(() => {
    const el = ldRef.current;
    if (!el) return;
    if (!q.data || q.data.kind !== 'job') {
      el.textContent = '';
      return;
    }
    el.textContent = JSON.stringify(buildJobPostingLd(q.data));
  }, [q.data]);

  if (!route) return <NotFound kind={undefined} t={t} />;
  if (q.isLoading) return <Skeleton />;
  if (q.isError) return <LoadError onRetry={() => q.refetch()} t={t} />;
  if (!q.data) return <NotFound kind={inferKindFromPrefix(route.prefix)} t={t} />;

  const snap = q.data;
  const expired = isoInPast(snap.deadline) || isoInPast(snap.expires_at);
  const canApply = !!snap.apply_url && !expired;

  const primaryCategory = snap.categories?.[0];

  return (
    <div className="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <script ref={ldRef} type="application/ld+json" />

      {/* Meta-style split: listing left, outlined chat rail right (xl+) */}
      <div className="flex flex-col gap-8 xl:flex-row xl:items-start xl:gap-8 xl:gap-x-12">
        <article className="min-w-0 flex-1 xl:max-w-3xl xl:pr-2">
          <Breadcrumbs prefix={route.prefix} category={primaryCategory} t={t} />

          {expired && (
            <div
              className="mt-4 rounded-md border border-amber-300 bg-amber-50 px-4 py-2 text-sm text-amber-900"
              role="status"
            >
              {expiredMessage(snap.kind, t)}
            </div>
          )}

          <header className="mt-4 flex items-start gap-4">
            <IssuingEntityAvatar snap={snap} />
            <div className="min-w-0 flex-1">
              <h1 className="text-2xl font-bold text-gray-900 sm:text-3xl">
                {snap.title}
                {snap.kind && getTypeMeta(snap.kind) && (
                  <span className="ml-3 inline-flex items-center gap-1.5 rounded-full bg-gray-100 px-3 py-1 text-xs font-medium text-gray-600 align-middle">
                    <Icon name={getTypeMeta(snap.kind)!.iconName} size={12} />
                    {t(getTypeMeta(snap.kind)!.labelKey)}
                  </span>
                )}
              </h1>
              <p className="mt-1 text-sm text-gray-700">
                <span className="font-medium">{snap.issuing_entity}</span>
              </p>
              <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-gray-600">
                {snap.anchor_location?.city && <span>{snap.anchor_location.city}</span>}
                {snap.anchor_location?.region && <span>{snap.anchor_location.region}</span>}
                {snap.anchor_location?.country && <span>{snap.anchor_location.country}</span>}
                {snap.remote && (
                  <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs">
                    {t('job.remote')}
                  </span>
                )}
                {snap.posted_at && (
                  <span className="text-gray-500">
                    {t('job.postedOn')} {timeAgo(snap.posted_at)}
                  </span>
                )}
                {snap.deadline && !expired && (
                  <span className="text-orange-700">
                    {deadlineLabel(snap.kind, t)} {new Date(snap.deadline).toLocaleDateString()}
                  </span>
                )}
              </div>
              <div className="mt-4 flex flex-wrap items-center gap-3">
                {canApply && <ApplyLink snap={snap} mountedAtRef={mountedAtRef} t={t} />}
                <ShareButton title={snap.title} subtitle={snap.issuing_entity} t={t} />
              </div>
            </div>
          </header>

          <Suspense fallback={<BodyFallback />}>
            {isJob(snap) && <JobBody snap={snap} />}
            {isScholarship(snap) && <ScholarshipBody snap={snap} />}
            {isTender(snap) && <TenderBody snap={snap} />}
            {isDeal(snap) && <DealBody snap={snap} />}
            {isFunding(snap) && <FundingBody snap={snap} />}
          </Suspense>

          <HowToApplySection
            opportunityId={snap.id}
            slug={snap.slug}
            hasHowToApply={snap.has_how_to_apply}
          />

          {canApply && (
            <div className="mt-12 flex justify-center">
              <ApplyLink snap={snap} mountedAtRef={mountedAtRef} t={t} large />
            </div>
          )}
        </article>

        <OpportunitySideChat snap={snap} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function ApplyLink({
  snap,
  mountedAtRef,
  t,
  large = false,
}: {
  snap: OpportunitySnapshot;
  mountedAtRef: { current: number };
  t: (k: StringKey, fallback?: string) => string;
  large?: boolean;
}) {
  const { hasSession, ready, login } = useAuth();
  const className = large ? 'btn-primary px-8 py-3 text-base' : 'btn-primary';
  const label = hasSession ? applyCtaLabel(snap.kind, t) : t('cta.signInToApply');

  const track = () => {
    trackApplyClick({
      canonical_job_id: snap.id,
      slug: snap.slug,
      company: snap.issuing_entity,
      apply_url: snap.apply_url ?? '',
      dwell_ms: Math.round(
        (typeof performance !== 'undefined' ? performance.now() : Date.now()) - mountedAtRef.current
      ),
    });
  };

  const openEmployer = () => {
    track();
    pingApply(snap.slug);
    if (snap.apply_url) {
      window.open(snap.apply_url, '_blank', 'noopener,noreferrer');
    }
  };

  const signInThenApply = () => {
    // Stash apply intent on the current listing so OIDC returnTo restores it.
    try {
      const url = new URL(window.location.href);
      url.searchParams.set('apply', '1');
      window.history.replaceState({}, '', url.pathname + url.search + url.hash);
    } catch {
      /* ignore */
    }
    void login();
  };

  // Wait for auth resolve so we don't flash "Apply" before session restore.
  if (!ready) {
    return (
      <span className={`${className} pointer-events-none opacity-60`} aria-busy="true">
        {t('cta.signInToApply')}
      </span>
    );
  }

  if (!hasSession) {
    return (
      <button type="button" onClick={signInThenApply} className={className}>
        {label}
        {!large && (
          <svg
            className="ml-1.5 h-4 w-4"
            viewBox="0 0 20 20"
            fill="currentColor"
            aria-hidden="true"
          >
            <path
              fillRule="evenodd"
              d="M3 4.25A2.25 2.25 0 015.25 2h5.5A2.25 2.25 0 0113 4.25v2a.75.75 0 01-1.5 0v-2a.75.75 0 00-.75-.75h-5.5a.75.75 0 00-.75.75v11.5c0 .414.336.75.75.75h5.5a.75.75 0 00.75-.75v-2a.75.75 0 011.5 0v2A2.25 2.25 0 0110.75 18h-5.5A2.25 2.25 0 013 15.75V4.25z"
              clipRule="evenodd"
            />
            <path
              fillRule="evenodd"
              d="M6 10a.75.75 0 01.75-.75h9.546l-1.048-.943a.75.75 0 111.004-1.114l2.5 2.25a.75.75 0 010 1.114l-2.5 2.25a.75.75 0 11-1.004-1.114l1.048-.943H6.75A.75.75 0 016 10z"
              clipRule="evenodd"
            />
          </svg>
        )}
      </button>
    );
  }

  return (
    <button type="button" onClick={openEmployer} className={className}>
      {label}
      {!large && (
        <svg className="ml-1.5 h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
          <path d="M11 3a1 1 0 100 2h2.586l-6.293 6.293a1 1 0 101.414 1.414L15 6.414V9a1 1 0 102 0V4a1 1 0 00-1-1h-5z" />
          <path d="M5 5a2 2 0 00-2 2v8a2 2 0 002 2h8a2 2 0 002-2v-3a1 1 0 10-2 0v3H5V7h3a1 1 0 100-2H5z" />
        </svg>
      )}
    </button>
  );
}

function applyCtaLabel(
  kind: OpportunityKind,
  t: (k: StringKey, fallback?: string) => string
): string {
  switch (kind) {
    case 'deal':
      return t('cta.redeemNow');
    case 'tender':
      return t('cta.submitBid');
    case 'scholarship':
    case 'funding':
    case 'job':
    default:
      return t('cta.applyNow');
  }
}

function deadlineLabel(
  kind: OpportunityKind,
  t: (k: StringKey, fallback?: string) => string
): string {
  switch (kind) {
    case 'tender':
      return t('deadline.closes');
    case 'deal':
      return t('deadline.expires');
    default:
      return t('deadline.applyBy');
  }
}

function expiredMessage(
  kind: OpportunityKind,
  t: (k: StringKey, fallback?: string) => string
): string {
  switch (kind) {
    case 'scholarship':
      return t('expired.scholarship');
    case 'tender':
      return t('expired.tender');
    case 'deal':
      return t('expired.deal');
    case 'funding':
      return t('expired.funding');
    case 'job':
    default:
      return t('expired.job');
  }
}

function inferKindFromPrefix(prefix: string): OpportunityKind | undefined {
  switch (prefix) {
    case 'jobs':
      return 'job';
    case 'scholarships':
      return 'scholarship';
    case 'tenders':
      return 'tender';
    case 'deals':
      return 'deal';
    case 'funding':
      return 'funding';
    default:
      return undefined;
  }
}

function Breadcrumbs({
  prefix,
  category,
  t,
}: {
  prefix: string;
  category?: string;
  t: (k: StringKey, fallback?: string) => string;
}) {
  return (
    <nav aria-label="Breadcrumb" className="text-sm text-gray-500">
      <a href="/" className="hover:text-gray-700">
        {t('common.home')}
      </a>
      <span className="mx-1.5">/</span>
      <a href={`/${prefix}/`} className="capitalize hover:text-gray-700">
        {prefix}
      </a>
      {category && (
        <>
          <span className="mx-1.5">/</span>
          <a href={`/categories/${encodeURIComponent(category)}/`} className="hover:text-gray-700">
            {categoryLabel(category)}
          </a>
        </>
      )}
    </nav>
  );
}

function IssuingEntityAvatar({ snap }: { snap: OpportunitySnapshot }) {
  const logo =
    typeof snap.attributes?.logo_url === 'string'
      ? (snap.attributes.logo_url as string)
      : undefined;
  if (logo) {
    return (
      <img
        src={logo}
        alt={`${snap.issuing_entity} logo`}
        className="h-14 w-14 shrink-0 rounded-lg border border-gray-200 object-contain bg-white"
        loading="lazy"
      />
    );
  }
  const initial = (snap.issuing_entity || '?').trim().slice(0, 1).toUpperCase();
  return (
    <div
      className="flex h-14 w-14 shrink-0 items-center justify-center rounded bg-navy-100 text-xl font-semibold text-navy-900"
      aria-hidden="true"
    >
      {initial}
    </div>
  );
}

function ShareButton({
  title,
  subtitle,
  t,
}: {
  title: string;
  subtitle: string;
  t: (k: StringKey, fallback?: string) => string;
}) {
  const canShare = typeof navigator !== 'undefined' && 'share' in navigator;
  async function onClick() {
    const url = window.location.href;
    if (canShare) {
      try {
        await navigator.share({ title, text: `${title} — ${subtitle}`, url });
        return;
      } catch {
        // fall through to clipboard fallback
      }
    }
    try {
      await navigator.clipboard.writeText(url);
    } catch {
      // clipboard blocked — noop
    }
  }
  return (
    <button
      type="button"
      onClick={onClick}
      className="inline-flex items-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
    >
      <svg
        className="mr-1.5 h-4 w-4"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 24 24"
        aria-hidden="true"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="2"
          d="M8.684 13.342C8.886 12.938 9 12.482 9 12c0-.482-.114-.938-.316-1.342m0 2.684a3 3 0 110-2.684m0 2.684l6.632 3.316m-6.632-6l6.632-3.316m0 0a3 3 0 105.367-2.684 3 3 0 00-5.367 2.684zm0 9.316a3 3 0 105.368 2.684 3 3 0 00-5.368-2.684z"
        />
      </svg>
      {canShare ? t('cta.share') : t('cta.copyLink')}
    </button>
  );
}

function buildJobPostingLd(snap: OpportunitySnapshot): Record<string, unknown> {
  const ld: Record<string, unknown> = {
    '@context': 'https://schema.org',
    '@type': 'JobPosting',
    title: snap.title,
    description: snap.description_html ?? snap.description,
    datePosted: snap.posted_at,
    validThrough: snap.expires_at ?? snap.deadline,
    employmentType:
      typeof snap.attributes?.employment_type === 'string'
        ? snap.attributes.employment_type
        : undefined,
    hiringOrganization: {
      '@type': 'Organization',
      name: snap.issuing_entity,
      logo: typeof snap.attributes?.logo_url === 'string' ? snap.attributes.logo_url : undefined,
    },
  };
  if (snap.anchor_location) {
    ld.jobLocation = {
      '@type': 'Place',
      address: {
        '@type': 'PostalAddress',
        addressLocality: snap.anchor_location.city,
        addressRegion: snap.anchor_location.region,
        addressCountry: snap.anchor_location.country,
      },
    };
  }
  if (snap.amount_min || snap.amount_max) {
    const period =
      typeof snap.attributes?.salary_period === 'string'
        ? (snap.attributes.salary_period as string)
        : 'year';
    ld.baseSalary = {
      '@type': 'MonetaryAmount',
      currency: snap.currency || 'USD',
      value: {
        '@type': 'QuantitativeValue',
        minValue: snap.amount_min,
        maxValue: snap.amount_max,
        unitText: period.toUpperCase(),
      },
    };
  }
  return ld;
}

function Skeleton() {
  return (
    <div className="mx-auto max-w-3xl px-4 py-8 sm:px-6 lg:px-8">
      <div className="animate-pulse space-y-3">
        <div className="h-5 w-32 rounded bg-slate-200" />
        <div className="h-8 w-2/3 rounded bg-slate-200" />
        <div className="h-4 w-1/2 rounded bg-slate-200" />
        <div className="mt-6 h-40 rounded-lg bg-slate-100" />
      </div>
    </div>
  );
}

function BodyFallback() {
  return (
    <div className="mt-8 animate-pulse space-y-3">
      <div className="h-4 w-full rounded bg-slate-100" />
      <div className="h-4 w-5/6 rounded bg-slate-100" />
      <div className="h-4 w-2/3 rounded bg-slate-100" />
    </div>
  );
}

function NotFound({
  kind,
  t,
}: {
  kind: OpportunityKind | undefined;
  t: (k: StringKey, fallback?: string) => string;
}) {
  const label = kind ?? 'opportunity';
  const browseHref = kind ? `/${pluralForKind(kind)}/` : '/jobs/';
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900 capitalize">
        {label} {t('error.notFound')}
      </h1>
      <p className="mt-2 text-gray-600">{t('error.listingRemoved')}</p>
      <a href={browseHref} className="btn-primary mt-6">
        {t('cta.browseAll')}
      </a>
    </div>
  );
}

function pluralForKind(kind: OpportunityKind): string {
  switch (kind) {
    case 'job':
      return 'jobs';
    case 'scholarship':
      return 'scholarships';
    case 'tender':
      return 'tenders';
    case 'deal':
      return 'deals';
    case 'funding':
      return 'funding';
  }
}

function LoadError({
  onRetry,
  t,
}: {
  onRetry: () => void;
  t: (k: StringKey, fallback?: string) => string;
}) {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-xl font-semibold text-gray-900">{t('error.somethingWrong')}</h1>
      <p className="mt-2 text-gray-600">{t('error.couldNotLoad')}</p>
      <button type="button" onClick={onRetry} className="btn-primary mt-6">
        {t('cta.tryAgain')}
      </button>
    </div>
  );
}
