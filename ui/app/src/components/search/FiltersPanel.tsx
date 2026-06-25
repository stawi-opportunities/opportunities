import { categoryLabel } from '@/utils/format';
import type { FacetEntry, Facets, SearchParams } from '@/types/search';

const REMOTE_FACET_LABELS: Record<string, string> = {
  remote: 'Remote',
  hybrid: 'Hybrid',
  onsite: 'On-site',
  on_site: 'On-site',
};

export function FiltersPanel({
  params,
  setParams,
  facets,
  hasActiveFilters,
  onClear,
}: {
  params: SearchParams;
  setParams: (p: SearchParams) => void;
  facets: Facets | undefined;
  hasActiveFilters: boolean;
  onClear: () => void;
}) {
  return (
    <div className="md:sticky md:top-20">
      <div className="mb-4 flex items-center justify-between">
        <SortPicker
          value={params.sort ?? (params.q ? 'relevance' : 'recent')}
          onChange={(sort) => setParams({ ...params, sort })}
        />
        {hasActiveFilters && (
          <button
            type="button"
            onClick={onClear}
            className="hidden text-sm text-gray-600 hover:text-gray-900 md:inline"
          >
            Clear
          </button>
        )}
      </div>
      {facets && (
        <>
          <FacetBlock
            label="Category"
            entries={facets.category}
            selected={params.category}
            labeller={categoryLabel}
            onSelect={(v) => setParams({ ...params, category: v, offset: 0 })}
          />
          <FacetBlock
            label="Remote"
            entries={facets.remote_type}
            selected={params.remote_type}
            labeller={(k) => REMOTE_FACET_LABELS[k] ?? k}
            onSelect={(v) => setParams({ ...params, remote_type: v, offset: 0 })}
          />
          <FacetBlock
            label="Employment type"
            entries={facets.employment_type}
            selected={params.employment_type}
            onSelect={(v) => setParams({ ...params, employment_type: v, offset: 0 })}
          />
          <FacetBlock
            label="Seniority"
            entries={facets.seniority}
            selected={params.seniority}
            onSelect={(v) => setParams({ ...params, seniority: v, offset: 0 })}
          />
          <FacetBlock
            label="Country"
            entries={facets.country}
            selected={params.country}
            onSelect={(v) => setParams({ ...params, country: v, offset: 0 })}
          />
        </>
      )}
    </div>
  );
}

function SortPicker({
  value,
  onChange,
}: {
  value: SearchParams['sort'];
  onChange: (v: SearchParams['sort']) => void;
}) {
  return (
    <div>
      <label className="block text-xs font-semibold uppercase tracking-wider text-gray-500">
        Sort
      </label>
      <select
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value as SearchParams['sort'])}
        className="mt-1 rounded-md border border-gray-300 bg-white px-2 py-1 text-sm"
      >
        <option value="relevance">Relevance</option>
        <option value="recent">Most recent</option>
        <option value="quality">Highest quality</option>
        <option value="salary_high">Salary: high to low</option>
      </select>
    </div>
  );
}

function FacetBlock({
  label,
  entries,
  selected,
  labeller,
  onSelect,
}: {
  label: string;
  entries: FacetEntry[];
  selected: string | undefined;
  labeller?: (k: string) => string;
  onSelect: (v: string | undefined) => void;
}) {
  if (!entries.length) return null;
  const fmt = labeller ?? ((k: string) => k || '(uncategorised)');
  return (
    <div className="mb-5">
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wider text-gray-500">{label}</h3>
      <ul>
        {entries.slice(0, 8).map((e) => {
          const isSel = selected === e.key;
          return (
            <li key={e.key}>
              <button
                type="button"
                aria-pressed={isSel}
                className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-sm transition-colors ${
                  isSel ? 'bg-navy-50 font-medium text-navy-900' : 'text-gray-700 hover:bg-gray-50'
                }`}
                onClick={() => onSelect(isSel ? undefined : e.key)}
              >
                <span>{fmt(e.key)}</span>
                <span className={`text-xs ${isSel ? 'text-navy-700' : 'text-gray-400'}`}>
                  {e.count.toLocaleString()}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
