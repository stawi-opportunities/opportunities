import { useEffect, useMemo, useState } from 'react';
import Cascade from './Cascade';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import { useI18n } from '@/i18n/I18nProvider';
import type { SearchParams } from '@/types/search';
import { SortPicker } from '@/components/ui/SortPicker';

export default function LocaleShard() {
  const mount = useMemo(() => document.getElementById('mount-locale-shard'), []);
  const country = (mount?.getAttribute('data-locale-country') ?? '').toUpperCase();
  const langsCSV = mount?.getAttribute('data-locale-languages') ?? '';

  const languages = useMemo(
    () =>
      langsCSV
        .split(',')
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean),
    [langsCSV]
  );

  useEffect(() => {
    if (!country) return;
    const meta = document.createElement('meta');
    meta.name = 'visitor-locale';
    meta.content = JSON.stringify({ country, languages });
    document.head.appendChild(meta);
    return () => {
      meta.remove();
    };
  }, [country, languages]);

  const { preferredCountries, preferredLanguages } = useCandidateProfile();
  const [sort, setSort] = useState<SearchParams['sort']>('recent');

  return (
    <ShardStatusBanner country={country} languages={languages}>
      <div className="mb-4">
        <SortPicker value={sort} onChange={setSort} />
      </div>
      <Cascade
        filters={{ sort }}
        preferredCountries={preferredCountries}
        preferredLanguages={preferredLanguages}
        tierLimit={25}
        overrideCountry={country}
        overrideLanguages={languages}
      />
    </ShardStatusBanner>
  );
}

function ShardStatusBanner({
  country,
  languages,
  children,
}: {
  country: string;
  languages: string[];
  children: React.ReactNode;
}) {
  const { t } = useI18n();
  const [dismissed, setDismissed] = useState(false);
  if (dismissed || !country) return <>{children}</>;

  const langsSuffix = languages.length ? ` ${languages.join(', ')}` : '';
  const message = t('locale.showingJobs')
    .replace('{country}', country)
    .replace('{langs}', langsSuffix);

  return (
    <>
      <p
        role="status"
        className="mb-4 flex items-center justify-between rounded-md bg-emerald-50 px-4 py-2 text-sm text-emerald-800"
      >
        <span>{message}</span>
        <button
          type="button"
          onClick={() => setDismissed(true)}
          className="ml-4 text-xs font-medium text-emerald-900 underline hover:text-emerald-950"
        >
          {t('cta.dismiss')}
        </button>
      </p>
      {children}
    </>
  );
}
