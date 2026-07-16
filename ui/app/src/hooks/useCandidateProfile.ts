import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchCandidate, type CandidateSummary } from '@/api/profile';
import { useAuth } from '@/providers/AuthProvider';
import { QUERY_KEYS } from '@/constants/queryKeys';

// Splits a comma-or-semicolon-separated string into a trimmed,
// non-empty string array. Kept here so every caller of this hook
// gets the same normalisation without repeating the pattern.
function splitCSV(csv: string | undefined | null): string[] {
  if (!csv) return [];
  return csv
    .split(/[,;]/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export interface CandidateProfileResult {
  data: CandidateSummary | null | undefined;
  isLoading: boolean;
  preferredCountries: string[];
  preferredLanguages: string[];
}

/**
 * Fetches the authenticated candidate's profile and derives the two
 * preference arrays most components need for feed personalisation.
 *
 * - Only fires when the user is authenticated.
 * - Cache key is shared with all other callers via QUERY_KEYS so
 *   React Query deduplicates the request across islands.
 * - Returns empty arrays when unauthenticated or when the profile
 *   has no preference data yet.
 */
export function useCandidateProfile(): CandidateProfileResult {
  const { hasSession } = useAuth();

  const query = useQuery({
    queryKey: QUERY_KEYS.CANDIDATE_PROFILE,
    queryFn: fetchCandidate,
    enabled: hasSession,
    staleTime: 5 * 60_000,
    placeholderData: (prev) => prev,
  });

  const preferredCountries = useMemo(
    () => splitCSV(query.data?.preferred_countries),
    [query.data?.preferred_countries]
  );

  const preferredLanguages = useMemo(
    () => splitCSV(query.data?.languages),
    [query.data?.languages]
  );

  return {
    data: query.data,
    isLoading: query.isLoading,
    preferredCountries,
    preferredLanguages,
  };
}
