import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { categoryJobs } from "@/api/search";
import { JobRow } from "./JobRow";

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

  return (
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
      <h1 className="text-3xl font-bold capitalize">{slug}</h1>
      <p className="mt-2 text-gray-600">Jobs in this category.</p>

      {q.isLoading && <SkeletonList />}
      {q.isError && <p className="mt-8 text-red-700">Unable to load this category.</p>}
      {q.data && (
        <>
          <ul className="mt-6 divide-y divide-gray-200 rounded-lg border border-gray-200">
            {q.data.results.map((r) => (
              <JobRow key={r.id} result={r} />
            ))}
            {q.data.results.length === 0 && (
              <li className="px-4 py-6 text-center text-sm text-gray-500">
                No jobs in this category right now.
              </li>
            )}
          </ul>
          {q.data.has_more && (
            <div className="mt-6 text-center">
              <button
                type="button"
                className="rounded border border-gray-300 px-4 py-2 text-sm hover:bg-gray-50"
                onClick={() => setCursor(q.data.cursor_next)}
              >
                Load more
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
      {Array.from({ length: 4 }).map((_, i) => (
        <li key={i} className="h-20 animate-pulse rounded-lg bg-gray-100" />
      ))}
    </ul>
  );
}

function NotFound() {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">Category not found</h1>
      <a href="/categories/" className="mt-4 inline-block text-blue-600 hover:underline">
        Back to all categories
      </a>
    </div>
  );
}
