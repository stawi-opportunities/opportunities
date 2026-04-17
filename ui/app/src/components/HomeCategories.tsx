import { useQuery } from "@tanstack/react-query";
import { listCategories } from "@/api/search";

export default function HomeCategories() {
  const q = useQuery({
    queryKey: ["categories"],
    queryFn: () => listCategories(),
    staleTime: 5 * 60_000,
  });
  const cats = q.data?.categories ?? [];

  if (q.isLoading) {
    return (
      <div className="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        {Array.from({ length: 8 }).map((_, i) => (
          <div key={i} className="h-20 animate-pulse rounded-lg bg-gray-100" />
        ))}
      </div>
    );
  }

  if (cats.length === 0) {
    return <p className="mt-8 text-center text-sm text-gray-500">No categories yet.</p>;
  }

  return (
    <div className="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {cats.map((c) => (
        <a
          key={c.key}
          href={`/categories/${encodeURIComponent(c.key)}/`}
          className="flex items-center gap-4 rounded-lg border border-gray-200 p-4 hover:border-accent-300"
        >
          <div>
            <h3 className="font-semibold capitalize text-gray-900">
              {c.key || "Uncategorised"}
            </h3>
            <p className="text-sm text-gray-500">{c.count.toLocaleString()} jobs</p>
          </div>
        </a>
      ))}
    </div>
  );
}
