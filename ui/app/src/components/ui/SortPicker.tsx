import { useI18n } from '@/i18n/I18nProvider';
import type { SearchParams } from '@/types/search';

interface SortPickerProps {
  value: SearchParams['sort'];
  onChange: (v: SearchParams['sort']) => void;
}

export function SortPicker({ value, onChange }: SortPickerProps) {
  const { t } = useI18n();
  return (
    <div>
      <label className="block text-xs font-semibold uppercase tracking-wider text-gray-500">
        {t('search.sort')}
      </label>
      <select
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value as SearchParams['sort'])}
        className="mt-1 rounded-md border border-gray-300 bg-white px-2 py-1 text-sm"
      >
        <option value="relevance">{t('search.sortRelevance')}</option>
        <option value="recent">{t('search.sortRecent')}</option>
        <option value="quality">{t('search.sortQuality')}</option>
        <option value="salary_high">{t('search.sortSalary')}</option>
      </select>
    </div>
  );
}
