import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { getAuthRuntime, type AuthState } from "@stawi/auth-runtime";
import { getConfig } from "@/utils/config";
import { setAnalyticsUser } from "@/analytics/openobserve";

// Thin wrapper around the @stawi/auth-runtime singleton. A single
// AuthProvider at the root of every island means all islands share the
// same runtime instance (the widget uses a global symbol for this) and
// therefore the same token store and auth state subscription.

interface AuthCtx {
  state: AuthState;
  runtime: ReturnType<typeof getAuthRuntime>;
  login: () => Promise<void>;
  logout: () => Promise<void>;
}

const Ctx = createContext<AuthCtx | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const cfg = getConfig();

  const runtime = useMemo(
    () =>
      getAuthRuntime({
        clientId: cfg.oidcClientID,
        installationId: cfg.oidcInstallationID,
        idpBaseUrl: cfg.oidcIssuer,
        apiBaseUrl: cfg.candidatesAPIURL,
        redirectUri: cfg.oidcRedirectURI,
        scopes: ["openid", "profile", "offline_access"],
        skipFedCM: true,
      }),
    [
      cfg.oidcClientID,
      cfg.oidcInstallationID,
      cfg.oidcIssuer,
      cfg.candidatesAPIURL,
      cfg.oidcRedirectURI,
    ],
  );

  const [state, setState] = useState<AuthState>(runtime.getState());

  useEffect(() => {
    const unsub = runtime.onAuthStateChange((next) => {
      setState(next);
      // Thread the authenticated identity into OpenObserve so every
      // RUM session and log line is joinable back to profile_id.
      // `getUser()` is async and only resolves when a live token
      // exists, so guard on the state we just transitioned to.
      if (next === "authenticated") {
        runtime.getUser().then(
          (u) => setAnalyticsUser({ id: u.id, name: u.name, email: u.email }),
          () => setAnalyticsUser(null),
        );
      } else if (next === "unauthenticated") {
        setAnalyticsUser(null);
      }
    });

    // Kick the runtime out of "initializing" on first mount by asking for
    // a token silently. A rejection here means "no live session" — the
    // runtime emits an `unauthenticated` state as a side effect of the
    // failure, so we don't need to force it with logout() (which would
    // bounce fresh visitors to Hydra's end-session endpoint).
    if (runtime.getState() === "initializing") {
      runtime.getAccessToken().catch(() => {});
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
