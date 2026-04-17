import { useQuery } from "@tanstack/react-query";
import { statsSummary } from "@/api/search";

export default function HomeStats() {
  const q = useQuery({
    queryKey: ["stats-summary"],
    queryFn: () => statsSummary(),
    staleTime: 5 * 60_000,
  });

  const totalJobs = q.data?.total_jobs ?? 0;
  const totalCompanies = q.data?.total_companies ?? 0;

  return (
    <div className="mt-12 flex items-center justify-center gap-12 text-center">
      <Stat label="Jobs Posted" value={totalJobs} pending={q.isLoading} />
      <div className="h-8 w-px bg-white/20" aria-hidden="true" />
      <Stat label="Companies Hiring" value={totalCompanies} pending={q.isLoading} />
    </div>
  );
}

function Stat({ label, value, pending }: { label: string; value: number; pending: boolean }) {
  return (
    <div>
      <div className="text-3xl font-bold">
        {pending ? "—" : `${value.toLocaleString()}+`}
      </div>
      <div className="mt-1 text-sm text-gray-400">{label}</div>
    </div>
  );
}
