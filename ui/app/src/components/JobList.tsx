import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { searchJobs } from "@/api/search";
import { JobRow } from "./JobRow";

/** /jobs/ — generic "all jobs" list. Just recency, no filters. Use /search/ for filtered browsing. */
export default function JobList() {
  const [cursor, setCursor] = useState<string | undefined>();

  const q = useQuery({
    queryKey: ["all-jobs", cursor],
    queryFn: () => searchJobs({ sort: "recent", limit: 25, cursor }),
    staleTime: 30_000,
  });

  return (
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
      <h1 className="text-3xl font-bold">All jobs</h1>
      <p className="mt-2 text-gray-600">Everything posted, most recent first.</p>

      {q.isLoading && <SkeletonList />}
      {q.isError && <p className="mt-6 text-red-700">Unable to load jobs right now.</p>}
      {q.data && (
        <>
          <ul className="mt-6 divide-y divide-gray-200 rounded-lg border border-gray-200">
            {q.data.results.map((r) => (
              <JobRow key={r.id} result={r} />
            ))}
            {q.data.results.length === 0 && (
              <li className="px-4 py-6 text-center text-sm text-gray-500">No jobs yet.</li>
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
      {Array.from({ length: 6 }).map((_, i) => (
        <li key={i} className="h-20 animate-pulse rounded-lg bg-gray-100" />
      ))}
    </ul>
  );
}
