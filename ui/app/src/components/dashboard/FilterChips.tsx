import { useCallback } from 'react';
import type { StringKey } from '@/i18n/strings';
import { Icon } from '@/components/ui/Icon';
import { getTypeMeta } from '@/constants/opportunityTypes';

export const OPPORTUNITY_KINDS = [
  { value: '', labelKey: 'feed.all' as StringKey },
  { value: 'job', labelKey: 'kind.job' as StringKey },
  { value: 'scholarship', labelKey: 'kind.scholarship' as StringKey },
  { value: 'tender', labelKey: 'kind.tender' as StringKey },
  { value: 'deal', labelKey: 'kind.deal' as StringKey },
  { value: 'funding', labelKey: 'kind.funding' as StringKey },
] as const;

export interface FeedFilters {
  remote: boolean | null;
  kind: string;
}

function readFiltersFromURL(): FeedFilters {
  if (typeof window === 'undefined') return { remote: null, kind: '' };
  const params = new URL(window.location.href).searchParams;
  return {
    remote: params.has('remote') ? params.get('remote') === 'true' : null,
    kind: params.get('kind') ?? '',
  };
}

function writeFiltersToURL(filters: FeedFilters) {
  if (typeof window === 'undefined') return;
  const url = new URL(window.location.href);
  if (filters.remote === null) url.searchParams.delete('remote');
  else url.searchParams.set('remote', String(filters.remote));
  if (filters.kind) url.searchParams.set('kind', filters.kind);
  else url.searchParams.delete('kind');
  window.history.pushState({}, '', url.toString());
}

interface Props {
  filters: FeedFilters;
  onChange: (filters: FeedFilters) => void;
  t: (k: StringKey, fallback?: string) => string;
}

export function FilterChips({ filters, onChange, t }: Props) {
  const toggleRemote = useCallback(() => {
    const next = filters.remote === true ? null : filters.remote === false ? true : true;
    const newFilters = { ...filters, remote: next };
    writeFiltersToURL(newFilters);
    onChange(newFilters);
  }, [filters, onChange]);

  return (
    <div className="flex gap-2 overflow-x-auto overscroll-x-contain pb-1 [-ms-overflow-style:none] [scrollbar-width:none] sm:flex-wrap sm:overflow-visible [&::-webkit-scrollbar]:hidden">
      <button
        type="button"
        onClick={toggleRemote}
        className={`min-h-[40px] shrink-0 rounded-full px-3.5 py-2 text-xs font-medium transition-colors ${
          filters.remote !== null
            ? 'bg-accent-500 text-navy-950'
            : 'border border-muted-strong bg-surface text-secondary hover:bg-surface-hover'
        }`}
      >
        {filters.remote === true
          ? 'Remote'
          : filters.remote === false
            ? 'On-site'
            : 'Remote / On-site'}
      </button>

      {OPPORTUNITY_KINDS.map(({ value, labelKey }) => {
        const active = filters.kind === value;
        return (
          <button
            key={value}
            type="button"
            onClick={() => {
              const newFilters = { ...filters, kind: active ? '' : value };
              writeFiltersToURL(newFilters);
              onChange(newFilters);
            }}
            className={`inline-flex min-h-[40px] shrink-0 items-center rounded-full px-3.5 py-2 text-xs font-medium transition-colors ${
              active
                ? 'bg-accent-500 text-navy-950'
                : 'border border-muted-strong bg-surface text-secondary hover:bg-surface-hover'
            }`}
          >
            {value && getTypeMeta(value) && (
              <Icon name={getTypeMeta(value)!.iconName} size={12} className="mr-1" />
            )}
            {t(labelKey)}
          </button>
        );
      })}
    </div>
  );
}

export { readFiltersFromURL };
