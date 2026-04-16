// Auth page components for Stawi Jobs
// Login is handled by @stawi/auth-runtime (FedCM + OAuth2 popup fallback)
// These components provide the UI for the login and callback pages.

document.addEventListener("alpine:init", () => {
  window.oidcLogin = function () {
    return {
      loading: false,
      error: "",
      async startLogin() {
        this.loading = true;
        this.error = "";
        try {
          await Alpine.store("auth").login();
          // If login succeeded, check if user has a candidate profile
          const profile = Alpine.store("auth").profile;
          if (profile && profile.candidate) {
            window.location.href = "/dashboard/";
          } else {
            window.location.href = "/onboarding/";
          }
        } catch (e) {
          this.error = e.message || "Login failed. Please try again.";
          this.loading = false;
        }
      },
    };
  };

  // Callback page — auth-runtime handles popup callbacks internally,
  // but if the user lands here via redirect, show a message
  window.oidcCallback = function () {
    return {
      loading: true,
      error: "",
      async init() {
        // auth-runtime uses popup/FedCM, so direct callback navigation
        // shouldn't normally happen. Redirect to dashboard or login.
        const store = Alpine.store("auth");

        // Wait a moment for auth state to settle
        await new Promise((r) => setTimeout(r, 1000));

        if (store.isAuthenticated) {
          if (store.profile && store.profile.candidate) {
            window.location.href = "/dashboard/";
          } else {
            window.location.href = "/onboarding/";
          }
        } else {
          this.loading = false;
          this.error = "Authentication was not completed. Please try again.";
        }
      },
    };
  };
});
