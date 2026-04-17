import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { categoryJobs } from "@/api/search";
import { JobRow } from "./JobRow";
import { categoryLabel } from "@/utils/format";

export default function CategoryPage() {
  const slug = (() => {
    const m = window.location.pathname.match(/^\/categories\/([^/]+)\/?$/);
    return m ? decodeURIComponent(m[1]!) : null;
  })();
  const [cursor, setCursor] = useState<string | undefined>();

  const q = useQuery({
    queryKey: ["category-jobs", slug, cursor],
    queryFn: () => categoryJobs(slug!, { cursor, limit: 25 }),
    enabled: !!slug,
    staleTime: 30_000,
  });

  if (!slug) return <NotFound />;
  const title = categoryLabel(slug);

  return (
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
      <nav aria-label="Breadcrumb" className="text-sm text-gray-500">
        <a href="/" className="hover:text-gray-700">Home</a>
        <span className="mx-1.5">/</span>
        <a href="/categories/" className="hover:text-gray-700">Categories</a>
        <span className="mx-1.5">/</span>
        <span className="text-gray-700">{title}</span>
      </nav>
      <h1 className="mt-3 text-3xl font-bold text-gray-900">{title} jobs</h1>
      <p className="mt-2 text-gray-600">
        Latest roles in {title.toLowerCase()} — filtered by category and sorted
        by recency.
      </p>

      {q.isLoading && <SkeletonList />}
      {q.isError && (
        <div className="mt-8 rounded-md bg-red-50 p-4 text-sm text-red-700" role="alert">
          We couldn't load this category.{" "}
          <button
            type="button"
            onClick={() => q.refetch()}
            className="font-medium underline hover:text-red-800"
          >
            Retry
          </button>
        </div>
      )}
      {q.data && (
        <>
          <ul className="mt-6 overflow-hidden rounded-lg border border-gray-200 bg-white">
            {q.data.results.map((r) => (
              <JobRow key={r.id} result={r} />
            ))}
            {q.data.results.length === 0 && (
              <li className="px-4 py-10 text-center">
                <p className="text-sm text-gray-700">
                  No {title.toLowerCase()} roles are open right now.
                </p>
                <a
                  href="/jobs/"
                  className="mt-3 inline-block text-sm font-medium text-navy-700 hover:text-navy-900"
                >
                  Browse all jobs →
                </a>
              </li>
            )}
          </ul>
          {q.data.has_more && (
            <div className="mt-6 text-center">
              <button
                type="button"
                disabled={q.isFetching}
                className="rounded-md border border-gray-300 bg-white px-5 py-2 text-sm font-medium text-gray-700 shadow-sm hover:bg-gray-50 disabled:opacity-60"
                onClick={() => setCursor(q.data.cursor_next)}
              >
                {q.isFetching ? "Loading…" : "Load more"}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function SkeletonList() {
  return (
    <ul className="mt-6 space-y-2">
      {Array.from({ length: 6 }).map((_, i) => (
        <li key={i} className="h-20 animate-pulse rounded-lg bg-gray-100" />
      ))}
    </ul>
  );
}

function NotFound() {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">Category not found</h1>
      <p className="mt-2 text-gray-600">
        That category doesn't exist (yet).
      </p>
      <a
        href="/categories/"
        className="mt-6 inline-block rounded-md bg-navy-900 px-5 py-2 text-sm font-medium text-white hover:bg-navy-800"
      >
        Back to all categories
      </a>
    </div>
  );
}
