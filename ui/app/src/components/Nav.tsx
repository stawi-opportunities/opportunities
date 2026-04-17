import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { listCategories } from "@/api/search";
import { StawiAuth } from "./StawiAuth";
import { categoryLabel } from "@/utils/format";
import type { FacetEntry } from "@/types/search";

/**
 * Top-level navigation bar. Responsibilities:
 *   - Brand / primary links ("Find Jobs" dropdown, About, Pricing)
 *   - Mobile menu toggle (auto-closes on link click)
 *   - Mount slot for @stawi/profile's auth UI
 */
export default function Nav() {
  const [mobileOpen, setMobileOpen] = useState(false);
  const [findJobsOpen, setFindJobsOpen] = useState(false);
  const findJobsRef = useRef<HTMLDivElement | null>(null);

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

  // Categories are fetched lazily — only when the user actually opens the
  // Find Jobs dropdown or the mobile menu. Keeps the landing page free of
  // backend calls on first paint.
  const categoriesQuery = useQuery({
    queryKey: ["categories"],
    queryFn: () => listCategories(),
    staleTime: 5 * 60_000,
    enabled: findJobsOpen || mobileOpen,
  });
  const categories: FacetEntry[] = categoriesQuery.data?.categories ?? [];
  const closeMobile = () => setMobileOpen(false);

  return (
    <header className="sticky top-0 z-40 border-b border-gray-200 bg-white" role="banner">
      <nav
        className="mx-auto flex h-20 max-w-7xl items-center justify-between gap-6 px-6 sm:px-8 lg:px-12"
        aria-label="Main navigation"
      >
        <a
          href="/"
          className="flex items-center"
          aria-label="Stawi Jobs — home"
        >
          <img src="/images/logo.svg" alt="Stawi Jobs" height="40" className="h-10 w-auto" />
        </a>

        <div className="hidden items-center gap-6 md:flex">
          <div ref={findJobsRef} className="relative">
            <button
              type="button"
              onClick={() => setFindJobsOpen((o) => !o)}
              aria-expanded={findJobsOpen}
              aria-haspopup="true"
              aria-controls="find-jobs-menu"
              className="flex items-center gap-1 text-sm font-medium text-gray-700 hover:text-navy-900"
            >
              Find Jobs
              <ChevronIcon open={findJobsOpen} />
            </button>
            {findJobsOpen && (
              <div
                id="find-jobs-menu"
                className="absolute left-0 top-full mt-2 w-72 rounded-lg border border-gray-200 bg-white py-2 shadow-lg"
              >
                <a
                  href="/jobs/"
                  className="block px-4 py-2 text-sm font-medium text-gray-900 hover:bg-gray-50"
                >
                  All Jobs
                </a>
                <a
                  href="/search/"
                  className="block px-4 py-2 text-sm font-medium text-gray-900 hover:bg-gray-50"
                >
                  Advanced search
                </a>
                <div className="my-1 border-t border-gray-100" />
                {categories.slice(0, 10).map((c) => (
                  <a
                    key={c.key}
                    href={`/categories/${encodeURIComponent(c.key)}/`}
                    className="flex items-center justify-between px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
                  >
                    <span>{categoryLabel(c.key)}</span>
                    <span className="ml-2 text-xs text-gray-400">
                      {c.count.toLocaleString()}
                    </span>
                  </a>
                ))}
                {categories.length === 0 && categoriesQuery.isSuccess && (
                  <p className="px-4 py-2 text-sm text-gray-400">
                    Categories load once jobs are indexed.
                  </p>
                )}
              </div>
            )}
          </div>

          <a href="/about/" className="text-sm font-medium text-gray-700 hover:text-navy-900">
            About
          </a>
        </div>

        <div className="flex items-center gap-3">
          <StawiAuth />

          <button
            type="button"
            onClick={() => setMobileOpen((o) => !o)}
            className="rounded-lg p-2 text-gray-500 hover:bg-gray-100 md:hidden"
            aria-expanded={mobileOpen}
            aria-controls="mobile-menu"
            aria-label="Toggle navigation menu"
          >
            <BurgerIcon open={mobileOpen} />
          </button>
        </div>
      </nav>

      {mobileOpen && (
        <div
          id="mobile-menu"
          className="border-t border-gray-200 bg-white md:hidden"
          aria-label="Mobile navigation"
        >
          <div className="space-y-1 px-6 py-4 sm:px-8">
            <a
              href="/jobs/"
              onClick={closeMobile}
              className="block rounded-lg px-3 py-2 text-sm font-medium text-gray-900 hover:bg-gray-50"
            >
              All Jobs
            </a>
            <a
              href="/search/"
              onClick={closeMobile}
              className="block rounded-lg px-3 py-2 text-sm font-medium text-gray-900 hover:bg-gray-50"
            >
              Advanced search
            </a>
            {categories.slice(0, 10).map((c) => (
              <a
                key={c.key}
                href={`/categories/${encodeURIComponent(c.key)}/`}
                onClick={closeMobile}
                className="block rounded-lg px-3 py-2 text-sm text-gray-600 hover:bg-gray-50"
              >
                {categoryLabel(c.key)}
              </a>
            ))}
            <div className="my-2 border-t border-gray-100" />
            <a
              href="/about/"
              onClick={closeMobile}
              className="block rounded-lg px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              About
            </a>
          </div>
        </div>
      )}
    </header>
  );
}

function ChevronIcon({ open }: { open: boolean }) {
  return (
    <svg
      className={`h-4 w-4 transition-transform ${open ? "rotate-180" : ""}`}
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 9l-7 7-7-7" />
    </svg>
  );
}

function BurgerIcon({ open }: { open: boolean }) {
  return open ? (
    <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M6 18L18 6M6 6l12 12" />
    </svg>
  ) : (
    <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 6h16M4 12h16M4 18h16" />
    </svg>
  );
}
