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

/**
 * When the signed-in user lacks a complete matching profile (CV + aspirational
 * fields), redirect to onboarding chat before showing the dashboard shell.
 */
export function useMatchingProfileGate(): {
  checking: boolean;
  readiness: ProfileReadiness | null;
} {
  const { hasSession, ready: authReady } = useAuth();
  const [redirecting, setRedirecting] = useState(false);

  const cvQ = useQuery({
    queryKey: QUERY_KEYS.ME_CV,
    queryFn: fetchMeCV,
    enabled: authReady && hasSession,
    staleTime: 30_000,
  });

  const draftQ = useQuery({
    queryKey: QUERY_KEYS.ONBOARDING_DRAFT,
    queryFn: fetchOnboardingDraft,
    enabled: authReady && hasSession,
    staleTime: 30_000,
  });

  const settled = cvQ.isFetched && draftQ.isFetched;
  const loading = Boolean(
    hasSession && authReady && !settled && (cvQ.isLoading || draftQ.isLoading)
  );

  const readiness =
    hasSession && settled
      ? evaluateProfileReadiness(cvQ.data ?? null, draftQ.data?.fields ?? null)
      : null;

  useEffect(() => {
    if (!authReady || !hasSession) return;
    if (!settled || redirecting) return;
    if (!readiness || readiness.ready) return;
    setRedirecting(true);
    window.location.replace(ONBOARDING_CHAT_PATH);
  }, [authReady, hasSession, settled, readiness, redirecting]);

  return {
    checking: Boolean(
      hasSession && (loading || redirecting || (readiness != null && !readiness.ready))
    ),
    readiness,
  };
}
