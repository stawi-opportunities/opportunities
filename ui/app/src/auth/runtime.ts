import { createAuthRuntime, type AuthRuntime } from "@stawi/auth-runtime";
import { getConfig } from "@/utils/config";

// Module-level singleton. @stawi/auth-runtime 1.0+ doesn't manage a
// global instance itself (unlike 0.2.x), so callers share state by
// sharing an import. One instance across all React islands + any
// non-React module (e.g. API clients) that needs auth'd fetches.

let instance: AuthRuntime | null = null;

export function authRuntime(): AuthRuntime {
  if (instance) return instance;
  const cfg = getConfig();
  instance = createAuthRuntime({
    clientId: cfg.oidcClientID,
    installationId: cfg.oidcInstallationID,
    idpBaseUrl: cfg.oidcIssuer,
    apiBaseUrl: cfg.candidatesAPIURL,
    redirectUri: cfg.oidcRedirectURI,
    scopes: ["openid", "profile", "offline_access"],
    skipFedCM: true,
  });
  return instance;
}

// Test seam — resetting the singleton lets unit tests create a fresh
// runtime without the old one's state bleeding through.
export function __resetAuthRuntimeForTests(): void {
  instance?.destroy();
  instance = null;
}
