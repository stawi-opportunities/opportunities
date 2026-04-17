import { useQuery } from "@tanstack/react-query";
import { latestJobs } from "@/api/search";
import { JobRow } from "./JobRow";

export default function HomeLatestJobs() {
  const q = useQuery({
    queryKey: ["latest-jobs"],
    queryFn: () => latestJobs(8),
    staleTime: 60_000,
  });

  if (q.isLoading) {
    return (
      <ul className="mt-8 space-y-3">
        {Array.from({ length: 4 }).map((_, i) => (
          <li key={i} className="h-20 animate-pulse rounded-lg bg-gray-100" />
        ))}
      </ul>
    );
  }

  const rows = q.data?.results ?? [];
  if (rows.length === 0) {
    return <p className="mt-8 text-center text-sm text-gray-500">No jobs to show yet.</p>;
  }

  return (
    <ul className="mt-8 divide-y divide-gray-200 rounded-lg border border-gray-200">
      {rows.map((r) => (
        <JobRow key={r.id} result={r} />
      ))}
    </ul>
  );
}
