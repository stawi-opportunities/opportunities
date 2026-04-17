// Alpine.js global initialization for Stawi Jobs
// Uses @stawi/auth-runtime for OIDC authentication
import { getAuthRuntime, decodeJwtPayload } from "@stawi/auth-runtime";

document.addEventListener("alpine:init", () => {
  const params = document.querySelector("meta[name=site-params]");
  const config = params ? JSON.parse(params.content) : {};

  // Initialize auth runtime singleton.
  //
  // Values come from hugo.toml params (see [params] block) with
  // HUGO_PARAMS_* env overrides at build time for staging/production.
  // Fallbacks match the Thesa "Stawi Jobs Development" tenant.
  //
  // skipFedCM: Thesa's Ory Hydra does not publish a /.well-known/web-identity
  // config, so FedCM probes add ~1s per sign-in click for no benefit.
  const auth = getAuthRuntime({
    clientId: config.oidcClientID || "stawi-jobs-web-dev",
    installationId: config.oidcInstallationID || "d7gi6lkpf2t67dlsqrhg",
    idpBaseUrl: config.oidcIssuer || "https://oauth2.stawi.org",
    apiBaseUrl: config.candidatesAPIURL || "https://api.stawi.org",
    redirectUri: config.oidcRedirectURI,
    // Match the scopes this client is registered for in Thesa
    // (see service-authentication .../20260416_create_stawi_jobs_test_tenant.sql).
    // The widget's default ["openid","profile","email"] is rejected by Hydra.
    scopes: ["openid", "profile", "offline_access"],
    skipFedCM: true,
  });

  // Global auth store — reactive bridge between auth-runtime and Alpine.js
  Alpine.store("auth", {
    isAuthenticated: false,
    profile: null,
    name: "",
    initials: "",
    roles: [],
    _runtime: auth,

    init() {
      // Subscribe to auth state changes. We don't call auth.getUser() —
      // that widget helper hits /me on apiBaseUrl, which isn't a unified-API
      // route on api.stawi.org and would retry-spam 404s. Everything we need
      // for the nav badge is in the ID token payload.
      auth.onAuthStateChange(async (state) => {
        if (state === "authenticated") {
          this.isAuthenticated = true;
          try {
            const token = await auth.getAccessToken();
            const claims = token ? decodeJwtPayload(token) : {};
            const displayName =
              claims.name || claims.preferred_username || claims.email ||
              (claims.sub ? "User " + String(claims.sub).slice(0, 6) : "User");
            this.name = displayName;
            this.initials = displayName
              .split(/[\s@.]+/)
              .filter(Boolean)
              .map((w) => w[0])
              .join("")
              .toUpperCase()
              .slice(0, 2) || "U";
            this.roles = (await auth.getRoles()) || [];
          } catch (e) {
            console.debug("auth: could not read claims", e);
            this.name = "User";
            this.initials = "U";
          }
        } else if (state === "unauthenticated") {
          this.isAuthenticated = false;
          this.profile = null;
          this.name = "";
          this.initials = "";
          this.roles = [];
        }
      });
    },

    async login() {
      // Surface errors — callers want to know whether login actually worked
      // (e.g. the navbar button shouldn't navigate to the dashboard if the
      // popup was blocked or the user dismissed the flow).
      await auth.ensureAuthenticated();
    },

    async logout() {
      try {
        await auth.logout();
      } catch (e) {
        console.error("Logout failed:", e);
      }
      this.isAuthenticated = false;
      this.profile = null;
      this.name = "";
      this.initials = "";
      this.roles = [];
      window.location.href = "/";
    },

    hasRole(role) {
      return this.roles.includes(role);
    },
  });

  // API fetch wrapper — uses auth-runtime for token management
  window.apiFetch = async function (path, options = {}) {
    const baseURL = config.candidatesAPIURL || "";
    const headers = { ...options.headers };
    try {
      const token = await auth.getAccessToken();
      if (token) {
        headers["Authorization"] = "Bearer " + token;
      }
    } catch (e) {
      // No token available — proceed without auth
    }
    if (
      !headers["Content-Type"] &&
      options.body &&
      !(options.body instanceof FormData)
    ) {
      headers["Content-Type"] = "application/json";
    }
    return fetch(baseURL + path, { ...options, headers });
  };

  // Jobs API fetch (separate base URL, no auth needed)
  window.jobsApiFetch = async function (path, options = {}) {
    const baseURL = config.apiURL || "";
    return fetch(baseURL + path, options);
  };

  // Root component
  window.appRoot = function () {
    return {
      mobileMenuOpen: false,
      init() {
        Alpine.store("auth").init();
      },
    };
  };

  // Save job component
  window.saveJob = function (jobId) {
    return {
      saved: false,
      jobId,
      async init() {
        if (!Alpine.store("auth").isAuthenticated) return;
        try {
          const resp = await apiFetch("/saved-jobs");
          if (resp.ok) {
            const data = await resp.json();
            this.saved = (data.saved || []).some(
              (s) => s.canonical_job_id === this.jobId
            );
          }
        } catch (e) {
          // Ignore — not critical
        }
      },
      async toggle() {
        if (!Alpine.store("auth").isAuthenticated) {
          await Alpine.store("auth").login();
          return;
        }
        if (this.saved) {
          await apiFetch("/jobs/" + this.jobId + "/save", {
            method: "DELETE",
          });
          this.saved = false;
        } else {
          await apiFetch("/jobs/" + this.jobId + "/save", { method: "POST" });
          this.saved = true;
        }
      },
    };
  };
});
