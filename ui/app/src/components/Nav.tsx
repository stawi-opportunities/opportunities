import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { listCategories } from "@/api/search";
import { StawiAuth } from "./StawiAuth";
import type { FacetEntry } from "@/types/search";

/**
 * Top-level navigation bar (replaces the former Alpine/Hugo navbar partial).
 *
 * Responsibilities:
 *   - Brand / primary links ("Find Jobs" dropdown, About, Pricing)
 *   - Mobile menu toggle
 *   - Mount slot for @stawi/profile's auth UI (sign-in button / avatar badge)
 *
 * Auth state is owned entirely by @stawi/profile — this component does not
 * know whether the user is signed in.
 */
export default function Nav() {
  const [mobileOpen, setMobileOpen] = useState(false);
  const [findJobsOpen, setFindJobsOpen] = useState(false);
  const findJobsRef = useRef<HTMLDivElement | null>(null);

  // Close the Find Jobs dropdown on outside click or Escape.
  useEffect(() => {
    if (!findJobsOpen) return;
    const onClick = (e: MouseEvent) => {
      if (!findJobsRef.current?.contains(e.target as Node)) setFindJobsOpen(false);
    };
    const onEsc = (e: KeyboardEvent) => e.key === "Escape" && setFindJobsOpen(false);
    document.addEventListener("click", onClick);
    document.addEventListener("keydown", onEsc);
    return () => {
      document.removeEventListener("click", onClick);
      document.removeEventListener("keydown", onEsc);
    };
  }, [findJobsOpen]);

  const categoriesQuery = useQuery({
    queryKey: ["categories"],
    queryFn: () => listCategories(),
    staleTime: 5 * 60_000,
  });
  const categories: FacetEntry[] = categoriesQuery.data?.categories ?? [];

  return (
    <header className="sticky top-0 z-40 border-b border-gray-200 bg-white" role="banner">
      <nav
        className="mx-auto flex h-16 max-w-7xl items-center justify-between px-4 sm:px-6 lg:px-8"
        aria-label="Main navigation"
      >
        <a href="/" className="flex items-center gap-2 text-xl font-bold text-navy-900">
          STAWI JOBS
        </a>

        <div className="hidden items-center gap-6 md:flex">
          <div ref={findJobsRef} className="relative">
            <button
              type="button"
              onClick={() => setFindJobsOpen((o) => !o)}
              aria-expanded={findJobsOpen}
              aria-haspopup="true"
              className="flex items-center gap-1 text-sm font-medium text-gray-700 hover:text-navy-900"
            >
              Find Jobs
              <ChevronIcon />
            </button>
            {findJobsOpen && (
              <div
                role="menu"
                className="absolute left-0 top-full mt-2 w-64 rounded-lg border border-gray-200 bg-white py-2 shadow-lg"
              >
                <a
                  href="/jobs/"
                  className="block px-4 py-2 text-sm font-medium text-gray-900 hover:bg-gray-50"
                  role="menuitem"
                >
                  All Jobs
                </a>
                <div className="my-1 border-t border-gray-100" />
                {categories.slice(0, 10).map((c) => (
                  <a
                    key={c.key}
                    href={`/categories/${encodeURIComponent(c.key)}/`}
                    className="block px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
                    role="menuitem"
                  >
                    <span className="capitalize">{c.key || "(uncategorised)"}</span>
                    <span className="ml-2 text-xs text-gray-400">
                      {c.count.toLocaleString()}
                    </span>
                  </a>
                ))}
                {categories.length === 0 && categoriesQuery.isSuccess && (
                  <p className="px-4 py-2 text-sm text-gray-400">No categories yet.</p>
                )}
              </div>
            )}
          </div>

          <a href="/about/" className="text-sm font-medium text-gray-700 hover:text-navy-900">
            About
          </a>
          <a href="/pricing/" className="text-sm font-medium text-gray-700 hover:text-navy-900">
            Pricing
          </a>
        </div>

        <div className="flex items-center gap-4">
          <StawiAuth />

          <button
            type="button"
            onClick={() => setMobileOpen((o) => !o)}
            className="rounded-lg p-2 text-gray-500 hover:bg-gray-100 md:hidden"
            aria-expanded={mobileOpen}
            aria-label="Toggle navigation menu"
          >
            <BurgerIcon />
          </button>
        </div>
      </nav>

      {mobileOpen && (
        <div className="border-t border-gray-200 bg-white md:hidden" aria-label="Mobile navigation">
          <div className="space-y-1 px-4 py-3">
            <a href="/jobs/" className="block rounded-lg px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">
              All Jobs
            </a>
            {categories.slice(0, 10).map((c) => (
              <a
                key={c.key}
                href={`/categories/${encodeURIComponent(c.key)}/`}
                className="block rounded-lg px-3 py-2 text-sm capitalize text-gray-600 hover:bg-gray-50"
              >
                {c.key || "(uncategorised)"}
              </a>
            ))}
            <a href="/about/" className="block rounded-lg px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">
              About
            </a>
            <a href="/pricing/" className="block rounded-lg px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50">
              Pricing
            </a>
          </div>
        </div>
      )}
    </header>
  );
}

function ChevronIcon() {
  return (
    <svg className="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 9l-7 7-7-7" />
    </svg>
  );
}

function BurgerIcon() {
  return (
    <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 6h16M4 12h16M4 18h16" />
    </svg>
  );
}
