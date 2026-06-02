import { useEffect, useMemo } from "react";
import Cascade from "./Cascade";
import { useCandidateProfile } from "@/hooks/useCandidateProfile";
import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Cascade from './Cascade';
import { useAuth } from '@/providers/AuthProvider';
import { fetchCandidate } from '@/api/candidates';
import { useI18n } from '@/i18n/I18nProvider';

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
  const auth = useAuth();
  const profile = useQuery({
    queryKey: ['candidate-profile'],
    queryFn: fetchCandidate,
    enabled: auth.state === 'authenticated',
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

  return (
    <ShardStatusBanner country={country} languages={languages}>
      <Cascade
        filters={{ sort: 'recent' }}
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

