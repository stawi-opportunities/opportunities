import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { AuthRuntime, AuthState } from "@stawi/auth-runtime";
import { authRuntime } from "@/auth/runtime";
import { setAnalyticsUser } from "@/analytics/openobserve";

// Thin context around the module-level runtime singleton. Every React
// island that's wrapped in <AuthProvider> gets the same instance, and
// non-React modules (api clients) can read it via authRuntime().

interface AuthCtx {
  state: AuthState;
  runtime: AuthRuntime;
  login: () => Promise<void>;
  logout: () => Promise<void>;
}

const Ctx = createContext<AuthCtx | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const runtime = useMemo(() => authRuntime(), []);
  const [state, setState] = useState<AuthState>(runtime.getState());

  useEffect(() => {
    const unsub = runtime.onAuthStateChange((next) => {
      setState(next);
      // Thread identity into OpenObserve so RUM + logs are joinable
      // back to the profile_id. The 1.0 runtime doesn't expose a
      // synchronous getUser() — use getClaims() once authenticated.
      if (next === "authenticated") {
        runtime.getClaims().then(
          (claims) => {
            const id = String(claims.sub ?? "");
            if (id) {
              setAnalyticsUser({
                id,
                name: String(claims.name ?? claims.preferred_username ?? ""),
                email: String(claims.email ?? ""),
              });
            }
          },
          () => setAnalyticsUser(null),
        );
      } else if (next === "unauthenticated") {
        setAnalyticsUser(null);
      }
    });

    // Warm the OIDC discovery cache and let the runtime move out of
    // "initializing" on mount. prefetchDiscovery is cheap (single GET
    // to /.well-known/openid-configuration) and fires a state
    // transition so the listener above updates React.
    if (runtime.getState() === "initializing") {
      runtime.prefetchDiscovery().catch(() => {
        // Discovery fetch failure surfaces via the normal auth-state
        // path — don't double-log.
      });
    }

    return unsub;
  }, [runtime]);

  const value = useMemo<AuthCtx>(
    () => ({
      state,
      runtime,
      login: () => runtime.ensureAuthenticated(),
      logout: () => runtime.logout(),
    }),
    [state, runtime],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAuth(): AuthCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}
