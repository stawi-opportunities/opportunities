// Dev-only AuthRuntime shim.
//
// When the production OIDC issuer is broken (or you don't want to
// touch it for local testing), append ?devauth=1 to any UI URL. The
// authRuntime() factory in runtime.ts detects the flag and returns
// this shim instead of @stawi/auth-runtime's real implementation.
//
// The shim:
//   - Reports state="authenticated" immediately (no OIDC dance)
//   - Forges a JWT with sub="local-test-user" for getClaims
//   - Attaches that forged JWT as Authorization: Bearer on every fetch
//   - Targets matching's gateway-trust assumption — matching's
//     profileIDFromJWT base64-decodes the payload without verifying
//     the signature, so this works as a real Bearer credential
//     against /pairings, /candidates/me/sessions/*, etc.
//
// The matching backend's candidate_profiles must have a row whose
// profile_id matches this sub (see scripts/seed-dev-candidate.sh or
// scripts/dev-pair.sh which seed cnd_dev_1 / sub=local-test-user).
//
// CHECK BEFORE PRODUCTION: this file is import-time inert until
// useDevAuth() returns true. The check is opt-in via URL param so a
// production deploy never accidentally lands on the shim. Still, do
// not ship this file to a static build that serves over a real
// domain.

import type { AuthRuntime, AuthState } from "@stawi/auth-runtime";
import { getConfig } from "@/utils/config";

const DEV_SUB = "local-test-user";

// useDevAuth returns true when ?devauth=1 is on the current URL OR a
// stashed flag is in sessionStorage (so the flag survives the OIDC
// callback redirect, which we don't actually use in dev mode but the
// pattern means a manual reload doesn't drop the flag).
export function useDevAuth(): boolean {
  if (typeof window === "undefined") return false;
  const url = new URL(window.location.href);
  if (url.searchParams.get("devauth") === "1") {
    sessionStorage.setItem("stawi:devauth", "1");
    return true;
  }
  return sessionStorage.getItem("stawi:devauth") === "1";
}

function b64url(s: string): string {
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function forgedJWT(sub: string): string {
  const header = b64url(JSON.stringify({ alg: "none", typ: "JWT" }));
  const payload = b64url(JSON.stringify({ sub, iss: "local-dev", aud: "stawi" }));
  return `${header}.${payload}.dev-signature`;
}

/**
 * Build a fake AuthRuntime that satisfies the interface our UI uses.
 * Methods we don't actually exercise return safe no-op values.
 */
export function createDevAuthRuntime(): AuthRuntime {
  const apiBaseUrl = getConfig().candidatesAPIURL.replace(/\/$/, "");
  const token = forgedJWT(DEV_SUB);

  const listeners = new Set<(s: AuthState) => void>();

  // Notify "authenticated" on the next tick so any onAuthStateChange
  // subscriber registered before/after this call still fires.
  queueMicrotask(() => {
    for (const l of listeners) l("authenticated");
  });

  return {
    version: "dev-shim",
    getState: (): AuthState => "authenticated",
    prefetchDiscovery: async () => {},
    ensureAuthenticated: async () => {},
    completeRedirect: async () => ({ returnTo: "/" }),
    logout: async () => {
      sessionStorage.removeItem("stawi:devauth");
      const url = new URL(window.location.href);
      url.searchParams.delete("devauth");
      window.location.href = url.toString();
    },
    onAuthStateChange: (cb) => {
      listeners.add(cb);
      // Fire immediately for new subscribers — they expect to learn
      // the current state synchronously.
      queueMicrotask(() => cb("authenticated"));
      return () => listeners.delete(cb);
    },
    onSecurityEvent: () => () => {},
    onFedcmEvent: () => () => {},
    getRoles: async () => [],
    getClaims: async () => ({ sub: DEV_SUB, name: "Local Test User", email: "dev@stawi.test" }),
    fetch: async <T = unknown>(path: string, init?: {
      method?: string;
      headers?: Record<string, string>;
      body?: string | ArrayBuffer | null;
    }): Promise<T> => {
      const res = await window.fetch(apiBaseUrl + path, {
        method: init?.method ?? "GET",
        headers: {
          Accept: "application/json",
          ...(init?.headers ?? {}),
          Authorization: `Bearer ${token}`,
        },
        body: init?.body ?? undefined,
      });
      const text = await res.text();
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${text}`);
      }
      if (!text) return null as T;
      try {
        return JSON.parse(text) as T;
      } catch {
        return text as unknown as T;
      }
    },
    upload: async <T = unknown>(path: string, file: File): Promise<T> => {
      const res = await window.fetch(apiBaseUrl + path, {
        method: "PUT",
        headers: {
          "Content-Type": file.type || "application/octet-stream",
          Authorization: `Bearer ${token}`,
        },
        body: file,
      });
      const text = await res.text();
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${text}`);
      }
      if (!text) return null as T;
      return JSON.parse(text) as T;
    },
    destroy: () => {
      listeners.clear();
    },
  };
}
