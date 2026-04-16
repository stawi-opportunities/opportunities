// Alpine.js global initialization for Stawi Jobs
document.addEventListener("alpine:init", () => {
  const params = document.querySelector("meta[name=site-params]");
  const config = params ? JSON.parse(params.content) : {};

  // Global auth store
  Alpine.store("auth", {
    isAuthenticated: false,
    accessToken: null,
    profile: null,
    name: "",
    initials: "",
    roles: [],

    async init() {
      const token = sessionStorage.getItem("stawi_access_token");
      if (token && !isTokenExpired(token)) {
        this.accessToken = token;
        this.isAuthenticated = true;
        await this.fetchProfile();
      }
    },

    setToken(token) {
      this.accessToken = token;
      this.isAuthenticated = true;
      sessionStorage.setItem("stawi_access_token", token);
    },

    async fetchProfile() {
      try {
        const resp = await apiFetch("/me");
        if (resp.ok) {
          const data = await resp.json();
          this.profile = data;
          this.name = data.name || "User";
          this.initials = this.name
            .split(" ")
            .map((w) => w[0])
            .join("")
            .toUpperCase()
            .slice(0, 2);
          this.roles = data.roles || [];
        }
      } catch (e) {
        console.error("Failed to fetch profile:", e);
      }
    },

    hasRole(role) {
      return this.roles.includes(role);
    },

    logout() {
      this.accessToken = null;
      this.isAuthenticated = false;
      this.profile = null;
      this.name = "";
      this.initials = "";
      this.roles = [];
      sessionStorage.removeItem("stawi_access_token");
      window.location.href = "/";
    },
  });

  // API fetch wrapper
  window.apiFetch = async function (path, options = {}) {
    const baseURL = config.candidatesAPIURL || "";
    const token = Alpine.store("auth").accessToken;
    const headers = { ...options.headers };
    if (token) {
      headers["Authorization"] = "Bearer " + token;
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

  // Jobs API fetch (separate base URL)
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
          window.location.href = "/auth/login/";
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

function isTokenExpired(token) {
  try {
    const payload = JSON.parse(atob(token.split(".")[1]));
    return payload.exp * 1000 < Date.now();
  } catch {
    return true;
  }
}
