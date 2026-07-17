import { categoryLabel } from '@/utils/format';
import type { FacetEntry, Facets, SearchParams } from '@/types/search';
import { SortPicker } from '@/components/ui/SortPicker';
import { useI18n } from '@/i18n/I18nProvider';

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
  const { t } = useI18n();
  const REMOTE_FACET_LABELS: Record<string, string> = {
    remote: t('search.remote'),
    hybrid: t('search.hybrid'),
    onsite: t('search.onSite'),
    on_site: t('search.onSite'),
  };
  return (
    <div className="md:sticky md:top-20">
      <div className="mb-4 flex items-center justify-between">
        <SortPicker
          value={params.sort ?? 'recent'}
          onChange={(sort) => setParams({ ...params, sort })}
        />
        {hasActiveFilters && (
          <button
            type="button"
            onClick={onClear}
            className="hidden text-sm text-gray-600 hover:text-gray-900 md:inline"
          >
            {t('search.clear')}
          </button>
        )}
      </div>
      {facets && (
        <>
          <FacetBlock
            label={t('search.category')}
            entries={facets.category}
            selected={params.category}
            labeller={categoryLabel}
            onSelect={(v) => setParams({ ...params, category: v, offset: 0 })}
          />
          <FacetBlock
            label={t('search.remote')}
            entries={facets.remote_type}
            selected={params.remote_type}
            labeller={(k) => REMOTE_FACET_LABELS[k] ?? k}
            onSelect={(v) => setParams({ ...params, remote_type: v, offset: 0 })}
          />
          <FacetBlock
            label={t('search.employmentType')}
            entries={facets.employment_type}
            selected={params.employment_type}
            onSelect={(v) => setParams({ ...params, employment_type: v, offset: 0 })}
          />
          <FacetBlock
            label={t('search.seniority')}
            entries={facets.seniority}
            selected={params.seniority}
            onSelect={(v) => setParams({ ...params, seniority: v, offset: 0 })}
          />
          <FacetBlock
            label={t('search.country')}
            entries={facets.country}
            selected={params.country}
            onSelect={(v) => setParams({ ...params, country: v, offset: 0 })}
          />
        </>
      )}
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
