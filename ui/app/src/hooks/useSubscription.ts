import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { fetchMeSubscription, type MeSubscription } from '@/api/profile';
import { useAuth } from '@/providers/AuthProvider';
import { QUERY_KEYS } from '@/constants/queryKeys';

/**
 * Fetches the authenticated candidate's subscription status.
 *
 * Enabled whenever we have a live session — including `refreshing` — so a
 * token refresh does not disable the query and wipe dashboard data.
 */
export function useSubscription(): UseQueryResult<MeSubscription> {
  const { hasSession } = useAuth();

  return useQuery({
    queryKey: QUERY_KEYS.SUBSCRIPTION,
    queryFn: fetchMeSubscription,
    enabled: hasSession,
    staleTime: 60_000,
    // Keep prior data while refetching / during brief auth transitions.
    placeholderData: (prev) => prev,
  });
}
