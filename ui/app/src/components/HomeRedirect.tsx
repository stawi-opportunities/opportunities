import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';
import { useUserContext } from '@/hooks/useUserContext';
import { pathMatchesStageHome } from '@/utils/userStage';
import { safeReplace } from '@/utils/safeNavigate';

/**
 * Marketing homepage (`/`): signed-in users leave the marketing shell
 * using the canonical user stage home path (never invent local rules).
 */
export default function HomeRedirect() {
  const { hasSession, ready, state } = useAuth();
  const ctx = useUserContext({ loadProfile: true });

  useEffect(() => {
    const hero = document.getElementById('home-hero');

    if (!ready) {
      if (hasSession && hero) hero.style.display = 'none';
      return;
    }

    if (!hasSession || state === 'unauthenticated') {
      if (hero) hero.style.display = '';
      return;
    }

    if (hero) hero.style.display = 'none';

    if (ctx.stage === 'loading' || ctx.resolving) return;

    const home = ctx.homePath;
    if (home === '/') return;
    if (pathMatchesStageHome(window.location.pathname, home)) return;
    safeReplace(home);
  }, [hasSession, ready, state, ctx.stage, ctx.homePath, ctx.resolving]);

  return null;
}
