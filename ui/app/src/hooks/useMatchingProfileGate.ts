import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchMeCV } from '@/api/profile';
import { fetchOnboardingDraft } from '@/api/candidates';
import { useAuth } from '@/providers/AuthProvider';
import { QUERY_KEYS } from '@/constants/queryKeys';
import {
  evaluateProfileReadiness,
  ONBOARDING_CHAT_PATH,
  type ProfileReadiness,
} from '@/utils/profileReadiness';

export type MatchingProfileGateOptions = {
  /**
   * When false, CV/draft are not fetched and the gate does not redirect.
   * Use after subscription is confirmed — unpaid users never load dashboard profile data.
   */
  enabled?: boolean;
};

/**
 * When the signed-in user lacks a complete matching profile (CV + aspirational
 * fields), redirect to onboarding chat before showing the dashboard shell.
 *
 * Only runs when `enabled` (default true). Dashboard passes `enabled` only after
 * subscription is allowed so unpaid users never trigger these loads.
 */
export function useMatchingProfileGate(options: MatchingProfileGateOptions = {}): {
  checking: boolean;
  readiness: ProfileReadiness | null;
} {
  const enabled = options.enabled !== false;
  const { hasSession, ready: authReady } = useAuth();
  const [redirecting, setRedirecting] = useState(false);
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

  useEffect(() => {
    if (!active) return;
    if (!settled || redirecting) return;
    if (!readiness || readiness.ready) return;
    setRedirecting(true);
    window.location.replace(ONBOARDING_CHAT_PATH);
  }, [active, settled, readiness, redirecting]);

  if (!enabled) {
    return { checking: false, readiness: null };
  }

  return {
    checking: Boolean(
      hasSession && (loading || redirecting || (readiness != null && !readiness.ready))
    ),
    readiness,
  };
}
