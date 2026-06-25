import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { fetchMeSubscription, type MeSubscription } from '@/api/profile';
import { useAuth } from '@/providers/AuthProvider';
import { QUERY_KEYS } from '@/constants/queryKeys';

/**
 * Fetches the authenticated candidate's subscription status.
 *
 * Returns the full React Query result so callers can branch on
 * `.data`, `.isLoading`, and `.isError` as needed. The underlying
 * `fetchMeSubscription` never throws — it returns a fallback shape
 * on any error — so callers only need to check `.isError` for UI
 * degradation signals, not for error recovery.
 *
 * Only fires when the user is authenticated.
 */
export function useSubscription(): UseQueryResult<MeSubscription> {
  const { state } = useAuth();

  return useQuery({
    queryKey: QUERY_KEYS.SUBSCRIPTION,
    queryFn: fetchMeSubscription,
    enabled: state === 'authenticated',
    staleTime: 60_000,
  });
}
