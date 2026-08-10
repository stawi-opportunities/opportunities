/**
 * Auth bridge for the ATS SPA.
 *
 * OIDC: @stawi/auth-runtime owns tokens — use runtime.fetch for API calls.
 * Local: VITE_ATS_DEV_HEADERS=true uses X-Profile/Tenant/Partition headers.
 */

import { createAuthRuntime, type AuthRuntime, type AuthState } from "@stawi/auth-runtime";

export type AuthMode = "oidc" | "dev" | "none";

let runtime: AuthRuntime | null = null;

function env(name: string): string {
  const v = (import.meta.env as Record<string, string | undefined>)[name];
  return (v || "").trim();
}

export function authMode(): AuthMode {
  if (env("VITE_OIDC_ISSUER") && env("VITE_OIDC_CLIENT_ID")) return "oidc";
  if (env("VITE_ATS_DEV_HEADERS") === "true" || env("VITE_ATS_DEV_HEADERS") === "1") return "dev";
  return "none";
}

export function getAuthRuntime(): AuthRuntime | null {
  if (runtime) return runtime;
  if (authMode() !== "oidc") return null;
  const issuer = env("VITE_OIDC_ISSUER");
  const clientId = env("VITE_OIDC_CLIENT_ID");
  const installationId = env("VITE_OIDC_INSTALLATION_ID") || clientId;
  const redirectUri =
    env("VITE_OIDC_REDIRECT_URI") ||
    (typeof window !== "undefined" ? `${window.location.origin}/auth/callback/` : "");
  runtime = createAuthRuntime({
    clientId,
    installationId,
    idpBaseUrl: issuer,
    apiBaseUrl: env("VITE_API_BASE_URL") || window.location.origin,
    redirectUri,
    scopes: ["openid", "profile", "offline_access"],
    skipFedCM: true,
  });
  return runtime;
}

function isSessionPresent(state: AuthState): boolean {
  return state === "authenticated" || state === "refreshing";
}

export async function ensureAuthReady(): Promise<{ mode: AuthMode; signedIn: boolean }> {
  const mode = authMode();
  if (mode === "dev") {
    return { mode, signedIn: true };
  }
  if (mode !== "oidc") {
    return { mode, signedIn: false };
  }
  const rt = getAuthRuntime();
  if (!rt) return { mode, signedIn: false };

  // Complete OIDC callback if we landed with ?code=
  if (typeof window !== "undefined" && /[?&]code=/.test(window.location.search)) {
    try {
      const { returnTo } = await rt.completeRedirect();
      if (returnTo && returnTo !== window.location.href) {
        window.location.replace(returnTo);
        return { mode, signedIn: true };
      }
    } catch {
      /* fall through to state poll */
    }
  }

  for (let i = 0; i < 40; i++) {
    const st = rt.getState();
    if (isSessionPresent(st)) return { mode, signedIn: true };
    if (st === "unauthenticated") return { mode, signedIn: false };
    await new Promise((r) => setTimeout(r, 50));
  }
  return { mode, signedIn: isSessionPresent(rt.getState()) };
}

/** Start OIDC login (redirect or FedCM). */
export async function login(): Promise<void> {
  const rt = getAuthRuntime();
  if (!rt) throw new Error("OIDC not configured (set VITE_OIDC_ISSUER / VITE_OIDC_CLIENT_ID)");
  await rt.ensureAuthenticated();
}

export async function logout(): Promise<void> {
  const rt = getAuthRuntime();
  if (rt) await rt.logout();
}

/**
 * Authenticated JSON Connect call. Uses runtime.fetch under OIDC so the
 * worker attaches Bearer; falls back to window.fetch for dev headers.
 */
export async function authFetchJson<T>(
  path: string,
  init: { method?: string; headers?: Record<string, string>; body?: string },
): Promise<T> {
  const mode = authMode();
  if (mode === "oidc") {
    const rt = getAuthRuntime();
    if (!rt) throw new Error("auth runtime missing");
    return rt.fetch<T>(path, {
      method: init.method || "POST",
      headers: init.headers,
      body: init.body ?? null,
    });
  }
  const headers: Record<string, string> = {
    ...(init.headers || {}),
  };
  if (mode === "dev") {
    headers["X-Profile-ID"] = localStorage.getItem("ats_profile_id") || "dev-recruiter";
    headers["X-Tenant-ID"] = localStorage.getItem("ats_tenant_id") || "dev-tenant";
    headers["X-Partition-ID"] = localStorage.getItem("ats_partition_id") || "dev-partition";
  }
  const res = await fetch(path, {
    method: init.method || "POST",
    headers,
    body: init.body,
  });
  if (!res.ok) {
    let detail = await res.text();
    try {
      const j = JSON.parse(detail);
      detail = j.message || j.detail || j.title || detail;
    } catch {
      /* keep */
    }
    throw new Error(detail || `${res.status}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}
