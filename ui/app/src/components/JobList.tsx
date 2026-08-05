import { useMemo, useState } from 'react';
import Cascade from './Cascade';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import type { FeedParams, SearchParams } from '@/types/search';
import { SortPicker } from '@/components/ui/SortPicker';

type FilterChip = {
  label: string;
  key: 'remote_type' | 'employment_type' | 'seniority';
  value: string;
};

const CHIPS: FilterChip[] = [
  { label: 'Remote', key: 'remote_type', value: 'remote' },
  { label: 'Full-time', key: 'employment_type', value: 'full_time' },
  { label: 'Part-time', key: 'employment_type', value: 'part_time' },
  { label: 'Contract', key: 'employment_type', value: 'contract' },
  { label: 'Entry', key: 'seniority', value: 'entry' },
  { label: 'Senior', key: 'seniority', value: 'senior' },
];

export default function JobList() {
  const { preferredCountries, preferredLanguages } = useCandidateProfile();
  const [active, setActive] = useState<Partial<Record<FilterChip['key'], string>>>({});
  const [sort, setSort] = useState<SearchParams['sort']>('recent');

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

  const filters = useMemo<FeedParams>(() => ({ sort, ...active }), [sort, active]);

  const hasFilters = Object.keys(active).length > 0;

  return (
    <div className="mx-auto max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
      <div className="flex flex-col gap-6 lg:flex-row lg:items-start lg:gap-10">
        <div className="flex-1 min-w-0">
          <div className="flex items-center justify-between gap-4">
            <h1 className="text-3xl font-bold text-main">All jobs</h1>
            <a href="/onboarding/" className="btn-primary hidden sm:inline-flex">
              Create profile
            </a>
          </div>
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
                    'inline-flex items-center rounded-full border px-4 py-1.5 text-sm font-medium transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-500',
                    on
                      ? 'border-accent-500 bg-accent-500/10 text-accent-500'
                      : 'border-muted-strong bg-surface text-secondary hover:border-accent-500/50 hover:text-main',
                  ].join(' ')}
                >
                  {chip.label}
                </button>
              );
            })}
            {hasFilters && (
              <button
                type="button"
                onClick={() => setActive({})}
                className="ml-1 text-sm text-secondary hover:text-main underline"
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
        <aside className="hidden w-64 shrink-0 lg:block">
          <div className="rounded-xl border border-muted bg-surface p-5 shadow-sm">
            <h3 className="text-sm font-semibold text-main">Filters</h3>
            <div className="mt-4">
              <SortPicker value={sort} onChange={setSort} />
            </div>
            <div className="mt-4 space-y-4">
              {['remote_type', 'employment_type', 'seniority'].map((key) => (
                <div key={key}>
                  <label className="block text-xs font-medium uppercase tracking-wide text-secondary">
                    {key === 'remote_type'
                      ? 'Remote'
                      : key === 'employment_type'
                        ? 'Employment'
                        : 'Level'}
                  </label>
                  <div className="mt-2 flex flex-wrap gap-2">
                    {CHIPS.filter((c) => c.key === key).map((c) => {
                      const on = active[c.key as FilterChip['key']] === c.value;
                      return (
                        <button
                          key={c.value}
                          type="button"
                          onClick={() => toggle(c)}
                          aria-pressed={on}
                          className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${on ? 'border-accent-500 bg-accent-500/10 text-accent-500' : 'border-muted bg-surface-muted text-secondary hover:border-accent-500/50'}`}
                        >
                          {c.label}
                        </button>
                      );
                    })}
                  </div>
                </div>
              ))}
              {hasFilters && (
                <button
                  type="button"
                  onClick={() => setActive({})}
                  className="w-full min-h-[44px] rounded-md border border-muted py-1.5 text-xs font-medium text-secondary hover:border-muted-strong hover:text-main transition-colors"
                >
                  Clear all filters
                </button>
              )}
            </div>
          </div>
          <div className="mt-4 rounded-xl border border-muted bg-surface p-5 shadow-sm">
            <h3 className="text-sm font-semibold text-main">Looking for more?</h3>
            <p className="mt-1 text-xs text-secondary">
              Use full search for keyword, location, and salary filters.
            </p>
            <a href="/search/" className="btn-primary mt-3 inline-flex w-full px-4 py-2 text-xs">
              Advanced search →
            </a>
          </div>
        </aside>
      </div>
    </div>
  );
}
