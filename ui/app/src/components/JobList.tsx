import { useMemo, useState } from "react";
import Cascade from "./Cascade";
import { useCandidateProfile } from "@/hooks/useCandidateProfile";
import type { FeedParams } from "@/types/search";
import { useMemo } from 'react';
import Cascade from './Cascade';
import { useAuth } from '@/providers/AuthProvider';
import { useQuery } from '@tanstack/react-query';
import { fetchCandidate } from '@/api/candidates';

type FilterChip = { label: string; key: "remote_type" | "employment_type" | "seniority"; value: string };

const CHIPS: FilterChip[] = [
  { label: "Remote",    key: "remote_type",      value: "remote"    },
  { label: "Full-time", key: "employment_type",   value: "full_time" },
  { label: "Part-time", key: "employment_type",   value: "part_time" },
  { label: "Contract",  key: "employment_type",   value: "contract"  },
  { label: "Entry",     key: "seniority",         value: "entry"     },
  { label: "Senior",    key: "seniority",         value: "senior"    },
];

/** /jobs/ — tiered discovery feed with quick-filter chips. */
export default function JobList() {
  const { preferredCountries, preferredLanguages } = useCandidateProfile();
  const [active, setActive] = useState<Partial<Record<FilterChip["key"], string>>>({});

  function toggle(chip: FilterChip) {
    setActive((prev) => {
      const current = prev[chip.key];
      if (current === chip.value) {
        const next = { ...prev };
        delete next[chip.key];
        return next;
      }
      return { ...prev, [chip.key]: chip.value };
    });
  }

  const filters = useMemo<FeedParams>(
    () => ({ sort: "recent", ...active }),
    [active],
  const auth = useAuth();
  const authed = auth.state === 'authenticated';

  // Pull the candidate profile only if authenticated — its country /
  // language preferences drive the "preferred" tier. One lightweight
  // request that queries the candidates service's /me/profile.
  const profile = useQuery({
    queryKey: ['candidate-profile'],
    queryFn: fetchCandidate,
    enabled: authed,
    staleTime: 5 * 60_000,
  });

  const preferredCountries = useMemo(
    () => splitCSV(profile.data?.preferred_countries),
    [profile.data?.preferred_countries]
  );
  const preferredLanguages = useMemo(
    () => splitCSV(profile.data?.languages),
    [profile.data?.languages]
  );

  const hasFilters = Object.keys(active).length > 0;

  return (
    <div className="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <div className="flex flex-col gap-6 lg:flex-row lg:items-start lg:gap-10">
        {/* Main column */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center justify-between gap-4">
            <h1 className="text-3xl font-bold text-gray-900">All jobs</h1>
            <a href="/onboarding/" className="btn-primary hidden sm:inline-flex">Create profile</a>
          </div>

          {/* Quick-filter chips */}
          <div className="mt-5 flex flex-wrap items-center gap-2">
            {CHIPS.map((chip) => {
              const on = active[chip.key] === chip.value;
              return (
                <button
                  key={`${chip.key}-${chip.value}`}
                  type="button"
                  onClick={() => toggle(chip)}
                  aria-pressed={on}
                  className={[
                    "inline-flex items-center rounded-full border px-4 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-navy-500",
                    on
                      ? "border-navy-900 bg-navy-900 text-white"
                      : "border-gray-300 bg-white text-gray-700 hover:border-navy-700 hover:text-navy-900",
                  ].join(" ")}
                >
                  {chip.label}
                </button>
              );
            })}
            {hasFilters && (
              <button
                type="button"
                onClick={() => setActive({})}
                className="ml-1 text-sm text-gray-500 hover:text-gray-900 underline"
              >
                Clear
              </button>
            )}
          </div>

          <div className="mt-6">
            <Cascade
              filters={filters}
              preferredCountries={preferredCountries}
              preferredLanguages={preferredLanguages}
              tierLimit={25}
            />
          </div>
        </div>

        {/* Sidebar filters */}
        <aside className="hidden w-64 shrink-0 lg:block">
          <div className="rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
            <h3 className="text-sm font-semibold text-gray-800">Filters</h3>

            <div className="mt-4 space-y-4">
              <div>
                <label className="block text-xs font-medium uppercase tracking-wide text-gray-500">Remote</label>
                <div className="mt-2 flex flex-wrap gap-2">
                  {CHIPS.filter((c) => c.key === "remote_type").map((c) => {
                    const on = active[c.key] === c.value;
                    return (
                      <button
                        key={c.value}
                        type="button"
                        onClick={() => toggle(c)}
                        aria-pressed={on}
                        className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                          on ? "border-accent-600 bg-accent-50 text-accent-700" : "border-gray-200 bg-gray-50 text-gray-600 hover:border-accent-400"
                        }`}
                      >
                        {c.label}
                      </button>
                    );
                  })}
                </div>
              </div>

              <div>
                <label className="block text-xs font-medium uppercase tracking-wide text-gray-500">Employment</label>
                <div className="mt-2 flex flex-wrap gap-2">
                  {CHIPS.filter((c) => c.key === "employment_type").map((c) => {
                    const on = active[c.key] === c.value;
                    return (
                      <button
                        key={c.value}
                        type="button"
                        onClick={() => toggle(c)}
                        aria-pressed={on}
                        className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                          on ? "border-accent-600 bg-accent-50 text-accent-700" : "border-gray-200 bg-gray-50 text-gray-600 hover:border-accent-400"
                        }`}
                      >
                        {c.label}
                      </button>
                    );
                  })}
                </div>
              </div>

              <div>
                <label className="block text-xs font-medium uppercase tracking-wide text-gray-500">Level</label>
                <div className="mt-2 flex flex-wrap gap-2">
                  {CHIPS.filter((c) => c.key === "seniority").map((c) => {
                    const on = active[c.key] === c.value;
                    return (
                      <button
                        key={c.value}
                        type="button"
                        onClick={() => toggle(c)}
                        aria-pressed={on}
                        className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                          on ? "border-accent-600 bg-accent-50 text-accent-700" : "border-gray-200 bg-gray-50 text-gray-600 hover:border-accent-400"
                        }`}
                      >
                        {c.label}
                      </button>
                    );
                  })}
                </div>
              </div>

              {hasFilters && (
                <button
                  type="button"
                  onClick={() => setActive({})}
                  className="w-full rounded-md border border-gray-200 py-1.5 text-xs font-medium text-gray-500 hover:border-gray-400 hover:text-gray-900 transition-colors"
                >
                  Clear all filters
                </button>
              )}
            </div>
          </div>

          <div className="mt-4 rounded-xl border border-gray-200 bg-white p-5 shadow-sm">
            <h3 className="text-sm font-semibold text-gray-800">Looking for more?</h3>
            <p className="mt-1 text-xs text-gray-500">Use full search for keyword, location, and salary filters.</p>
            <a href="/search/" className="mt-3 inline-flex w-full items-center justify-center rounded-lg bg-navy-900 px-4 py-2 text-xs font-semibold text-white hover:bg-navy-800 transition-colors">
              Advanced search →
            </a>
          </div>
        </aside>
      </div>
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
      <h1 className="text-3xl font-bold">All jobs</h1>
      <p className="mt-2 text-gray-600">
        Most relevant first, based on your location and language.
      </p>
      <Cascade
        filters={{ sort: 'recent' }}
        preferredCountries={preferredCountries}
        preferredLanguages={preferredLanguages}
        tierLimit={25}
      />
    </div>
  );
}

