import { useQuery } from "@tanstack/react-query";
import { listCategories } from "@/api/search";
import { categoryLabel } from "@/utils/format";

// Simple emoji-glyph map — avoids shipping an icon library just for the
// home grid. Designers can swap to SVGs later without touching callers.
const CATEGORY_GLYPHS: Record<string, string> = {
  programming: "⌨",
  design: "✎",
  customer_support: "☎",
  marketing: "◈",
  sales: "◆",
  devops: "⚙",
  product: "◉",
  data: "▦",
  data_science: "▦",
  management: "⚐",
  other: "◯",
};

const CATEGORY_BLURBS: Record<string, string> = {
  programming: "Backend, frontend, mobile, and full-stack engineering.",
  design: "Product, UI/UX, brand, illustration, and research roles.",
  customer_support: "Customer success, support engineering, CX.",
  marketing: "Content, SEO, performance, brand, and growth.",
  sales: "Inbound, outbound, SDR, AE, and revenue ops.",
  devops: "SRE, platform, infrastructure, and security.",
  product: "PM, product ops, and product marketing.",
  data: "Analytics, data engineering, ML, and BI.",
  data_science: "Analytics, data engineering, ML, and BI.",
  management: "People leadership, directors, and executives.",
  other: "Operations, finance, HR, legal, and more.",
};

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
          <div key={i} className="h-28 animate-pulse rounded-lg bg-gray-100" />
        ))}
      </div>
    );
  }

  if (cats.length === 0) {
    return (
      <p className="mt-8 text-center text-sm text-gray-500">
        Categories appear as jobs are indexed. Check back soon.
      </p>
    );
  }

  return (
    <div className="mt-8 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {cats.map((c) => (
        <a
          key={c.key}
          href={`/categories/${encodeURIComponent(c.key)}/`}
          className="group flex items-start gap-4 rounded-lg border border-gray-200 bg-white p-5 transition-colors hover:border-accent-300 hover:bg-accent-50/40 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500"
        >
          <div
            className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md bg-navy-900 text-lg text-white"
            aria-hidden="true"
          >
            {CATEGORY_GLYPHS[c.key] ?? "•"}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-baseline justify-between gap-2">
              <h3 className="truncate font-semibold text-gray-900 group-hover:text-accent-700">
                {categoryLabel(c.key)}
              </h3>
              <span className="shrink-0 text-xs font-medium text-gray-500">
                {c.count.toLocaleString()}
              </span>
            </div>
            <p className="mt-1 text-sm text-gray-600">
              {CATEGORY_BLURBS[c.key] ?? "Open roles in this category."}
            </p>
          </div>
        </a>
      ))}
    </div>
  );
}
