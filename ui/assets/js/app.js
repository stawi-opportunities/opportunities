// Alpine.js global initialization for Stawi Jobs
// Uses @stawi/auth-runtime for OIDC authentication
import { getAuthRuntime } from "@stawi/auth-runtime";

document.addEventListener("alpine:init", () => {
  const params = document.querySelector("meta[name=site-params]");
  const config = params ? JSON.parse(params.content) : {};

  // Initialize auth runtime singleton. Fallbacks match the Thesa-registered
  // stawi-jobs realm (see ui/hugo.toml and the deployment spec). Concrete
  // values are provided by hugo.toml params / HUGO_PARAMS_* at build time.
  const auth = getAuthRuntime({
    clientId: config.oidcClientID || "stawi-jobs-web-dev",
    idpBaseUrl: config.oidcIssuer || "https://oauth2.stawi.org",
    apiBaseUrl: config.candidatesAPIURL || "https://api.stawi.org",
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
      // Subscribe to auth state changes
      auth.onAuthStateChange(async (state) => {
        if (state === "authenticated") {
          this.isAuthenticated = true;
          try {
            const user = await auth.getUser();
            this.name = user?.name || "User";
            this.initials = this.name
              .split(" ")
              .map((w) => w[0])
              .join("")
              .toUpperCase()
              .slice(0, 2);
            this.roles = (await auth.getRoles()) || [];
          } catch (e) {
            console.error("Failed to load user:", e);
          }

          // Load candidate domain data from our API
          try {
            const resp = await apiFetch("/me");
            if (resp.ok) {
              this.profile = await resp.json();
            }
          } catch (e) {
            // Non-fatal — profile may not exist yet (new user)
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
      try {
        await auth.ensureAuthenticated();
      } catch (e) {
        console.error("Login failed:", e);
      }
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
