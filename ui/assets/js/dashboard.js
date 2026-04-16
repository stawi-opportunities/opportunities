document.addEventListener("alpine:init", () => {
  window.dashboardApp = function () {
    return {
      view: "matches",
      matches: [],
      savedJobs: [],
      loading: true,
      error: "",
      profile: null,

      async init() {
        const store = Alpine.store("auth");
        if (!store.isAuthenticated) {
          window.location.href = "/auth/login/";
          return;
        }
        this.profile = store.profile?.candidate;
        await this.loadMatches();
      },

      async loadMatches() {
        this.loading = true;
        try {
          const candidateId = this.profile?.id;
          if (!candidateId) { this.loading = false; return; }
          const resp = await apiFetch(
            "/candidates/matches?candidate_id=" + candidateId + "&limit=50"
          );
          if (resp.ok) {
            const data = await resp.json();
            this.matches = data.matches || [];
          }
        } catch (e) {
          this.error = "Failed to load matches.";
        } finally {
          this.loading = false;
        }
      },

      async loadSaved() {
        this.loading = true;
        try {
          const resp = await apiFetch("/saved-jobs?limit=100");
          if (resp.ok) {
            const data = await resp.json();
            this.savedJobs = data.saved || [];
          }
        } catch (e) {
          this.error = "Failed to load saved jobs.";
        } finally {
          this.loading = false;
        }
      },

      async switchView(newView) {
        this.view = newView;
        this.error = "";
        if (newView === "matches") await this.loadMatches();
        else if (newView === "saved") await this.loadSaved();
      },

      matchScoreColor(score) {
        if (score >= 0.8) return "text-green-600";
        if (score >= 0.6) return "text-yellow-600";
        return "text-gray-500";
      },

      formatScore(score) {
        return Math.round(score * 100) + "%";
      },

      statusBadgeClass(status) {
        const map = {
          new: "bg-blue-100 text-blue-700",
          sent: "bg-gray-100 text-gray-700",
          viewed: "bg-yellow-100 text-yellow-700",
          applied: "bg-green-100 text-green-700",
        };
        return map[status] || "bg-gray-100 text-gray-700";
      },
    };
  };
});
