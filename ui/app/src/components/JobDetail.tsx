import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchSnapshot } from "@/api/snapshot";
import { categoryLabel, fmtMoney, isoInPast, timeAgo } from "@/utils/format";
import { mountSanitisedHTML } from "@/utils/html";
import { useI18n } from "@/i18n/I18nProvider";
import type { JobSnapshot } from "@/types/snapshot";

/**
 * /jobs/<slug>/ hydration. CF Pages rewrites every /jobs/* URL (except
 * /jobs/ itself) to /job/index.html, which ships this island. We read the
 * slug from window.location.pathname, fetch the R2 JobSnapshot, and render.
 */
export default function JobDetail() {
  const { lang, t } = useI18n();
  const slug = (() => {
    const m = window.location.pathname.match(/^\/jobs\/([^/]+)\/?$/);
    return m ? decodeURIComponent(m[1]!) : null;
  })();

  // Language is part of the cache key so a switch triggers a fresh fetch
  // without us having to manually invalidate.
  const q = useQuery({
    queryKey: ["snapshot", slug, lang],
    queryFn: () => fetchSnapshot(slug!, lang),
    enabled: !!slug,
    staleTime: 5 * 60_000,
  });

  const descRef = useRef<HTMLDivElement | null>(null);
  const ldRef = useRef<HTMLScriptElement | null>(null);
  useEffect(() => {
    const el = descRef.current;
    if (!el) return;
    el.replaceChildren();
    if (q.data?.description_html) mountSanitisedHTML(el, q.data.description_html);
  }, [q.data?.description_html]);

  // JSON-LD for Google for Jobs. Rendered via direct DOM textContent (not
  // dangerouslySetInnerHTML) so the </script> sequence can't break out of
  // the script block.
  useEffect(() => {
    const el = ldRef.current;
    if (!el) return;
    if (!q.data) {
      el.textContent = "";
      return;
    }
    el.textContent = JSON.stringify(buildJobPostingLd(q.data));
  }, [q.data]);

  if (!slug) return <NotFound />;
  if (q.isLoading) return <Skeleton />;
  if (q.isError) return <LoadError onRetry={() => q.refetch()} />;
  if (!q.data) return <NotFound />;

  const snap = q.data;
  const expired = isoInPast(snap.expires_at);
  const money = fmtMoney(
    snap.compensation?.min,
    snap.compensation?.max,
    snap.compensation?.currency,
    snap.compensation?.period,
  );
  const canApply = !!snap.apply_url && !expired;

  // Notice shown when the current UI language differs from the snapshot's
  // source locale — i.e. the user is reading an automated translation.
  // snap.language is undefined on pre-translation snapshots; fall back to
  // an English assumption so we don't show the notice spuriously.
  const showTranslatedNotice = !!snap.language && snap.language !== lang;

  return (
    <article className="mx-auto max-w-3xl px-4 py-8 sm:px-6 lg:px-8">
      <script ref={ldRef} type="application/ld+json" />

      <nav aria-label="Breadcrumb" className="text-sm text-gray-500">
        <a href="/" className="hover:text-gray-700">Home</a>
        <span className="mx-1.5">/</span>
        <a href="/jobs/" className="hover:text-gray-700">Jobs</a>
        {snap.category && (
          <>
            <span className="mx-1.5">/</span>
            <a
              href={`/categories/${encodeURIComponent(snap.category)}/`}
              className="hover:text-gray-700"
            >
              {categoryLabel(snap.category)}
            </a>
          </>
        )}
      </nav>

      {expired && (
        <div
          className="mt-4 rounded-md border border-amber-300 bg-amber-50 px-4 py-2 text-sm text-amber-900"
          role="status"
        >
          This job is no longer accepting applications.
        </div>
      )}

      {showTranslatedNotice && (
        <div
          className="mt-4 rounded-md border border-sky-200 bg-sky-50 px-4 py-2 text-sm text-sky-900"
          role="status"
        >
          {t("job.translatedNotice")}
        </div>
      )}

      <header className="mt-4 flex items-start gap-4">
        <CompanyAvatar snap={snap} />
        <div className="min-w-0 flex-1">
          <h1 className="text-2xl font-bold text-gray-900 sm:text-3xl">{snap.title}</h1>
          <p className="mt-1 text-sm text-gray-700">
            <span className="font-medium">{snap.company.name}</span>
            {snap.company.verified && (
              <span
                className="ml-2 inline-flex items-center text-xs font-medium text-accent-700"
                title="Verified employer"
              >
                ✓ Verified
              </span>
            )}
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-gray-600">
            {snap.location.text && <span>{snap.location.text}</span>}
            {snap.employment.type && (
              <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs">
                {snap.employment.type}
              </span>
            )}
            {snap.employment.seniority && (
              <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs">
                {snap.employment.seniority}
              </span>
            )}
            {snap.location.remote_type && (
              <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs">
                {snap.location.remote_type}
              </span>
            )}
            {money && (
              <span className="font-medium text-emerald-700">{money}</span>
            )}
            {snap.posted_at && (
              <span className="text-gray-400">Posted {timeAgo(snap.posted_at)}</span>
            )}
          </div>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            {canApply && (
              <a
                href={snap.apply_url}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center rounded-md bg-navy-900 px-5 py-2.5 text-sm font-semibold text-white hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-navy-900"
              >
                {t("cta.applyNow")}
                <span className="sr-only"> (opens in a new tab)</span>
                <svg className="ml-1.5 h-4 w-4" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
                  <path d="M11 3a1 1 0 100 2h2.586l-6.293 6.293a1 1 0 101.414 1.414L15 6.414V9a1 1 0 102 0V4a1 1 0 00-1-1h-5z" />
                  <path d="M5 5a2 2 0 00-2 2v8a2 2 0 002 2h8a2 2 0 002-2v-3a1 1 0 10-2 0v3H5V7h3a1 1 0 100-2H5z" />
                </svg>
              </a>
            )}
            <ShareButton title={snap.title} company={snap.company.name} />
          </div>
        </div>
      </header>

      {(snap.skills.required.length > 0 || snap.skills.nice_to_have.length > 0) && (
        <section className="mt-8" aria-labelledby="skills-heading">
          <h2 id="skills-heading" className="sr-only">Skills</h2>
          {snap.skills.required.length > 0 && (
            <>
              <h3 className="text-sm font-semibold text-gray-700">{t("job.skillsRequired")}</h3>
              <ul className="mt-2 flex flex-wrap gap-2">
                {snap.skills.required.map((s) => (
                  <li key={s} className="rounded border border-navy-200 bg-navy-50 px-3 py-1 text-sm text-navy-900">
                    {s}
                  </li>
                ))}
              </ul>
            </>
          )}
          {snap.skills.nice_to_have.length > 0 && (
            <>
              <h3 className="mt-4 text-sm font-semibold text-gray-700">{t("job.skillsNiceToHave")}</h3>
              <ul className="mt-2 flex flex-wrap gap-2">
                {snap.skills.nice_to_have.map((s) => (
                  <li key={s} className="rounded-full bg-gray-100 px-3 py-1 text-sm text-gray-700">
                    {s}
                  </li>
                ))}
              </ul>
            </>
          )}
        </section>
      )}

      <section
        ref={descRef}
        className="prose prose-slate mt-8 max-w-none"
        aria-label="Job description"
      />

      {canApply && (
        <div className="mt-12 flex justify-center">
          <a
            href={snap.apply_url}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center rounded-md bg-navy-900 px-8 py-3 text-base font-semibold text-white hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-navy-900"
          >
            Apply for this role
            <span className="sr-only"> (opens in a new tab)</span>
          </a>
        </div>
      )}
    </article>
  );
}

function CompanyAvatar({ snap }: { snap: JobSnapshot }) {
  if (snap.company.logo_url) {
    return (
      <img
        src={snap.company.logo_url}
        alt={`${snap.company.name} logo`}
        className="h-14 w-14 shrink-0 rounded-lg border border-gray-200 object-contain bg-white"
        loading="lazy"
      />
    );
  }
  const initial = (snap.company.name || "?").trim().slice(0, 1).toUpperCase();
  return (
    <div
      className="flex h-14 w-14 shrink-0 items-center justify-center rounded bg-navy-100 text-xl font-semibold text-navy-900"
      aria-hidden="true"
    >
      {initial}
    </div>
  );
}

function ShareButton({ title, company }: { title: string; company: string }) {
  const canShare = typeof navigator !== "undefined" && "share" in navigator;
  async function onClick() {
    const url = window.location.href;
    if (canShare) {
      try {
        await navigator.share({ title, text: `${title} at ${company}`, url });
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
      <svg className="mr-1.5 h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M8.684 13.342C8.886 12.938 9 12.482 9 12c0-.482-.114-.938-.316-1.342m0 2.684a3 3 0 110-2.684m0 2.684l6.632 3.316m-6.632-6l6.632-3.316m0 0a3 3 0 105.367-2.684 3 3 0 00-5.367 2.684zm0 9.316a3 3 0 105.368 2.684 3 3 0 00-5.368-2.684z" />
      </svg>
      {canShare ? "Share" : "Copy link"}
    </button>
  );
}

function buildJobPostingLd(snap: JobSnapshot): Record<string, unknown> {
  const ld: Record<string, unknown> = {
    "@context": "https://schema.org",
    "@type": "JobPosting",
    title: snap.title,
    description: snap.description_html,
    datePosted: snap.posted_at,
    validThrough: snap.expires_at,
    employmentType: snap.employment?.type,
    hiringOrganization: {
      "@type": "Organization",
      name: snap.company.name,
      logo: snap.company.logo_url,
    },
  };
  if (snap.location.text) {
    ld.jobLocation = {
      "@type": "Place",
      address: {
        "@type": "PostalAddress",
        addressLocality: snap.location.text,
        addressCountry: snap.location.country,
      },
    };
  }
  if (snap.compensation?.min || snap.compensation?.max) {
    ld.baseSalary = {
      "@type": "MonetaryAmount",
      currency: snap.compensation.currency || "USD",
      value: {
        "@type": "QuantitativeValue",
        minValue: snap.compensation.min,
        maxValue: snap.compensation.max,
        unitText: (snap.compensation.period || "year").toUpperCase(),
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

function NotFound() {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">Job not found</h1>
      <p className="mt-2 text-gray-600">
        This role has been removed or has expired.
      </p>
      <a
        href="/jobs/"
        className="mt-6 inline-block rounded-md bg-navy-900 px-5 py-2 text-sm font-medium text-white hover:bg-navy-800"
      >
        Browse all jobs
      </a>
    </div>
  );
}

function LoadError({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-xl font-semibold text-gray-900">Something went wrong</h1>
      <p className="mt-2 text-gray-600">
        We couldn't load this job right now.
      </p>
      <button
        type="button"
        onClick={onRetry}
        className="mt-6 rounded-md bg-navy-900 px-5 py-2 text-sm font-medium text-white hover:bg-navy-800"
      >
        Try again
      </button>
    </div>
  );
}
