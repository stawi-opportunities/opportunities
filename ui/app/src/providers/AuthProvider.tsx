import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import type { AuthRuntime, AuthState } from '@stawi/auth-runtime';
import { authRuntime } from '@/auth/runtime';
import { isSessionPresent } from '@/auth/session';
import { setAnalyticsUser } from '@/analytics/posthog';

// Thin context around the module-level runtime singleton. Every React
// island that's wrapped in <AuthProvider> gets the same instance, and
// non-React modules (api clients) can read it via authRuntime().

interface AuthCtx {
  /** Raw runtime state machine value. */
  state: AuthState;
  /**
   * Sticky session flag: true once we've seen authenticated (or are mid-refresh).
   * Only clears on a definitive unauthenticated transition. Prefer this for
   * "show avatar vs Sign in" decisions so token refresh never flickers UI.
   */
  hasSession: boolean;
  /** True until the runtime leaves initializing (first paint of definitive UI). */
  ready: boolean;
  runtime: AuthRuntime;
  login: () => Promise<void>;
  logout: () => Promise<void>;
}

const Ctx = createContext<AuthCtx | null>(null);

function writeAuthedHint(hasSession: boolean) {
  try {
    if (hasSession) localStorage.setItem('stawi.authed', '1');
    else localStorage.removeItem('stawi.authed');
  } catch {
    // private mode / storage blocked
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const runtime = useMemo(() => authRuntime(), []);
  const [state, setState] = useState<AuthState>(() => runtime.getState());
  // Seed sticky session from the sync localStorage hint so returning users
  // don't flash "Sign in" before IndexedDB session restore finishes.
  const [hasSession, setHasSession] = useState<boolean>(() => {
    const s = runtime.getState();
    if (isSessionPresent(s)) return true;
    try {
      return localStorage.getItem('stawi.authed') === '1';
    } catch {
      return false;
    }
  });
  const hasSessionRef = useRef(hasSession);
  hasSessionRef.current = hasSession;

  const applyState = useCallback(
    (next: AuthState) => {
      setState(next);

      if (isSessionPresent(next)) {
        setHasSession(true);
        writeAuthedHint(true);
      } else if (next === 'unauthenticated') {
        setHasSession(false);
        writeAuthedHint(false);
      }
      // initializing / error: keep sticky hasSession (no flicker)

      if (next === 'authenticated') {
        runtime.getClaims().then(
          (claims) => {
            const id = String(claims.sub ?? '');
            if (id) {
              setAnalyticsUser({
                id,
                name: String(claims.name ?? claims.preferred_username ?? ''),
                email: String(claims.email ?? ''),
              });
            }
          },
          () => setAnalyticsUser(null)
        );
      } else if (next === 'unauthenticated') {
        setAnalyticsUser(null);
      }
    },
    [runtime]
  );

  useEffect(() => {
    // Sync immediately — onAuthStateChange only fires *future* transitions,
    // so if the worker already restored tokens before this island mounted we
    // would otherwise stay stuck on the useState() snapshot.
    applyState(runtime.getState());

    const unsub = runtime.onAuthStateChange((next) => {
      applyState(next);
    });

    // Warm OIDC discovery. Does not change auth state by itself.
    if (runtime.getState() === 'initializing') {
      runtime.prefetchDiscovery().catch(() => {
        /* discovery failure surfaces via normal auth path */
      });
    }

    return unsub;
  }, [runtime, applyState]);

  const ready = state !== 'initializing';

  const value = useMemo<AuthCtx>(
    () => ({
      state,
      hasSession,
      ready,
      runtime,
      login: () => runtime.ensureAuthenticated(),
      logout: () => runtime.logout(),
    }),
    [state, hasSession, ready, runtime]
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAuth(): AuthCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error('useAuth must be used inside <AuthProvider>');
  return ctx;
}
