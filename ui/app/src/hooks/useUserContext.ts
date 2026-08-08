import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/providers/AuthProvider';
import { useSubscription } from '@/hooks/useSubscription';
import { fetchMeCV } from '@/api/profile';
import { fetchOnboardingDraft } from '@/api/candidates';
import { QUERY_KEYS } from '@/constants/queryKeys';
import { evaluateProfileReadiness, type ProfileReadiness } from '@/utils/profileReadiness';
import {
  isBillingReturnPath,
  resolveUserStage,
  type UserStage,
  type UserStageInfo,
} from '@/utils/userStage';

export type UserContext = UserStageInfo & {
  /** Full stage id for logging / data attributes. */
  stage: UserStage;
  subscriptionStatus: string | null;
  readiness: ProfileReadiness | null;
  /** True while we cannot yet trust stage (auth or first subscription fetch). */
  resolving: boolean;
};

/**
 * Builds the seeker user context once session is known.
 *
 * Stage is the single source of truth for which island the user is on
 * (onboarding intake / paywall / payment confirm / dashboard setup / ready).
 */
export function useUserContext(options?: { loadProfile?: boolean }): UserContext {
  const loadProfile = options?.loadProfile !== false;
  const { hasSession, ready: authReady } = useAuth();
  const subQ = useSubscription();

  const subLoading = Boolean(
    hasSession && subQ.data == null && (subQ.isLoading || subQ.isFetching || subQ.isPending)
  );
  const subError = Boolean(hasSession && subQ.isError && subQ.data == null);
  const entitled = subQ.data?.status === 'active' || subQ.data?.status === 'past_due';

  // Profile readiness: always when entitled (dashboard); optional for unpaid
  // so onboarding can distinguish intake vs paywall.
  const profileEnabled = Boolean(authReady && hasSession && loadProfile && !subLoading);

  const cvQ = useQuery({
    queryKey: QUERY_KEYS.ME_CV,
    queryFn: fetchMeCV,
    enabled: profileEnabled,
    staleTime: 30_000,
  });
  const draftQ = useQuery({
    queryKey: QUERY_KEYS.ONBOARDING_DRAFT,
    queryFn: fetchOnboardingDraft,
    enabled: profileEnabled,
    staleTime: 30_000,
  });

  const profileSettled = !profileEnabled || (cvQ.isFetched && draftQ.isFetched);
  const profileLoading = Boolean(profileEnabled && !profileSettled);
  const readiness =
    profileEnabled && profileSettled
      ? evaluateProfileReadiness(cvQ.data ?? null, draftQ.data?.fields ?? null)
      : null;

  const info = useMemo(
    () =>
      resolveUserStage({
        authReady,
        hasSession,
        subscriptionLoading: subLoading,
        subscriptionError: subError,
        subscriptionStatus: subQ.data?.status,
        billingReturn: isBillingReturnPath(),
        profileLoading,
        profileReady: readiness == null ? null : readiness.ready,
      }),
    [
      authReady,
      hasSession,
      subLoading,
      subError,
      subQ.data?.status,
      profileLoading,
      readiness?.ready,
    ]
  );

  return {
    ...info,
    subscriptionStatus: subQ.data?.status ?? null,
    readiness,
    resolving: info.stage === 'loading' || (entitled && profileLoading),
  };
}
