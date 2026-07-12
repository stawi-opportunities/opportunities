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
    <div className="flex flex-wrap items-center gap-2">
      <button
        type="button"
        onClick={toggleRemote}
        className={`rounded-full px-3 py-1 text-xs font-medium transition-colors ${
          filters.remote !== null
            ? 'bg-navy-900 text-white'
            : 'border border-gray-300 bg-white text-gray-600 hover:bg-gray-50 dark:border-navy-600 dark:bg-navy-800 dark:text-gray-300 dark:hover:bg-navy-700'
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
            className={`rounded-full px-3 py-1 text-xs font-medium transition-colors ${
              active
                ? 'bg-navy-900 text-white'
                : 'border border-gray-300 bg-white text-gray-600 hover:bg-gray-50 dark:border-navy-600 dark:bg-navy-800 dark:text-gray-300 dark:hover:bg-navy-700'
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
