import { useQuery } from '@tanstack/react-query';
import { fetchMeCV } from '@/api/profile';
import { fetchOnboardingDraft } from '@/api/candidates';
import { useAuth } from '@/providers/AuthProvider';
import { QUERY_KEYS } from '@/constants/queryKeys';
import { evaluateProfileReadiness, type ProfileReadiness } from '@/utils/profileReadiness';

export type MatchingProfileGateOptions = {
  /**
   * When false, CV/draft are not fetched.
   * Dashboard enables this only after subscription is allowed.
   */
  enabled?: boolean;
};

/**
 * Reports whether the signed-in user has a complete matching profile
 * (CV + aspirational fields). Does **not** hard-navigate.
 *
 * Hard redirects caused a loop:
 *   onboarding (paid → dashboard) ↔ dashboard (incomplete → onboarding)
 *
 * Incomplete profiles stay on the dashboard (CV hub / chat refine).
 * Unpaid users are routed only by the subscription gate.
 */
export function useMatchingProfileGate(options: MatchingProfileGateOptions = {}): {
  checking: boolean;
  readiness: ProfileReadiness | null;
} {
  const enabled = options.enabled !== false;
  const { hasSession, ready: authReady } = useAuth();
  const active = Boolean(enabled && authReady && hasSession);

  const cvQ = useQuery({
    queryKey: QUERY_KEYS.ME_CV,
    queryFn: fetchMeCV,
    enabled: active,
    staleTime: 30_000,
  });

  const draftQ = useQuery({
    queryKey: QUERY_KEYS.ONBOARDING_DRAFT,
    queryFn: fetchOnboardingDraft,
    enabled: active,
    staleTime: 30_000,
  });

  const settled = cvQ.isFetched && draftQ.isFetched;
  const loading = Boolean(active && !settled && (cvQ.isLoading || draftQ.isLoading));

  const readiness =
    active && settled
      ? evaluateProfileReadiness(cvQ.data ?? null, draftQ.data?.fields ?? null)
      : null;

  if (!enabled) {
    return { checking: false, readiness: null };
  }

  return {
    // Only block paint while first load is in flight — never for "not ready".
    checking: Boolean(hasSession && loading),
    readiness,
  };
}
