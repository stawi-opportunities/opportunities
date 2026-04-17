import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchSnapshot } from "@/api/snapshot";
import { fmtMoney, isoInPast } from "@/utils/format";
import { mountSanitisedHTML } from "@/utils/html";

/**
 * /jobs/<slug>/ hydration. CF Pages rewrites every /jobs/* URL to
 * /jobs/index.html, which ships this island. We read the slug from the
 * pathname, fetch the R2 JobSnapshot, and render it.
 */
export default function JobDetail() {
  const slug = (() => {
    const m = window.location.pathname.match(/^\/jobs\/([^/]+)\/?$/);
    return m ? decodeURIComponent(m[1]!) : null;
  })();

  const q = useQuery({
    queryKey: ["snapshot", slug],
    queryFn: () => fetchSnapshot(slug!),
    enabled: !!slug,
    staleTime: 5 * 60_000,
  });

  const descRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const el = descRef.current;
    if (!el) return;
    el.replaceChildren();
    if (q.data?.description_html) mountSanitisedHTML(el, q.data.description_html);
  }, [q.data?.description_html]);

  if (!slug) return <NotFound />;
  if (q.isLoading) return <Skeleton />;
  if (q.isError) return <LoadError />;
  if (!q.data) return <NotFound />;

  const snap = q.data;
  const expired = isoInPast(snap.expires_at);
  const money = fmtMoney(
    snap.compensation?.min,
    snap.compensation?.max,
    snap.compensation?.currency,
    snap.compensation?.period,
  );

  return (
    <article className="mx-auto max-w-3xl px-4 py-8 sm:px-6 lg:px-8">
      {expired && (
        <div className="mb-4 rounded border border-amber-300 bg-amber-100 px-4 py-2 text-amber-900">
          This job is no longer accepting applications.
        </div>
      )}

      <header className="mb-6">
        <p className="text-sm font-medium text-gray-500">{snap.company.name}</p>
        <h1 className="mt-1 text-3xl font-bold text-gray-900">{snap.title}</h1>
        <div className="mt-3 flex flex-wrap items-center gap-2 text-sm text-gray-600">
          {snap.location.text && <span>{snap.location.text}</span>}
          {snap.employment.type && (
            <span className="rounded bg-gray-100 px-2 py-0.5">{snap.employment.type}</span>
          )}
          {snap.employment.seniority && (
            <span className="rounded bg-gray-100 px-2 py-0.5">{snap.employment.seniority}</span>
          )}
          {money && <span className="font-medium text-gray-700">{money}</span>}
        </div>
      </header>

      {(snap.skills.required.length > 0 || snap.skills.nice_to_have.length > 0) && (
        <section className="mb-6">
          {snap.skills.required.length > 0 && (
            <>
              <h2 className="text-sm font-semibold text-gray-700">Required skills</h2>
              <ul className="mt-2 flex flex-wrap gap-2">
                {snap.skills.required.map((s) => (
                  <li key={s} className="rounded-full bg-blue-50 px-3 py-1 text-sm text-blue-800">{s}</li>
                ))}
              </ul>
            </>
          )}
          {snap.skills.nice_to_have.length > 0 && (
            <>
              <h2 className="mt-4 text-sm font-semibold text-gray-700">Nice to have</h2>
              <ul className="mt-2 flex flex-wrap gap-2">
                {snap.skills.nice_to_have.map((s) => (
                  <li key={s} className="rounded-full bg-gray-100 px-3 py-1 text-sm text-gray-700">{s}</li>
                ))}
              </ul>
            </>
          )}
        </section>
      )}

      <section ref={descRef} className="prose max-w-none" />

      {snap.apply_url && !expired && (
        <a
          href={snap.apply_url}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-6 inline-block rounded bg-blue-600 px-5 py-3 text-white hover:bg-blue-700"
        >
          Apply now
        </a>
      )}
    </article>
  );
}

function Skeleton() {
  return (
    <div className="mx-auto max-w-3xl px-4 py-8 sm:px-6 lg:px-8">
      <div className="animate-pulse space-y-3">
        <div className="h-7 w-2/3 rounded bg-slate-200" />
        <div className="h-4 w-full rounded bg-slate-200" />
        <div className="h-4 w-5/6 rounded bg-slate-200" />
      </div>
    </div>
  );
}

function NotFound() {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">Job not found</h1>
      <p className="mt-2 text-gray-600">This job has been removed or has expired.</p>
      <a href="/search/" className="mt-6 inline-block text-blue-600 hover:underline">
        Back to search
      </a>
    </div>
  );
}

function LoadError() {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <p className="text-red-700">Unable to load this job right now. Please try again.</p>
    </div>
  );
}
