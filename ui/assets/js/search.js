document.addEventListener("alpine:init", () => {
  window.searchPage = function () {
    return {
      query: "",
      mode: "keyword",
      semanticResults: [],
      semanticLoading: false,
      semanticError: "",

      init() {
        const el = document.getElementById("pagefind-search");
        if (el && window.PagefindUI) {
          new PagefindUI({
            element: "#pagefind-search",
            pageSize: 20,
            showImages: false,
            showSubResults: false,
            showEmptyFilters: false,
            debounceTimeoutMs: 300,
            translations: {
              placeholder: "Search jobs by title, skill, or company...",
              zero_results: "No jobs found for [SEARCH_TERM]",
              many_results: "[COUNT] jobs found",
              one_result: "1 job found",
              filters: "Filters",
              load_more: "Load more results",
            },
          });
        }

        const params = new URLSearchParams(window.location.search);
        if (params.has("q")) {
          this.query = params.get("q");
        }
      },

      async searchSemantic() {
        if (!this.query.trim()) return;
        this.semanticLoading = true;
        this.semanticError = "";
        this.semanticResults = [];

        try {
          const resp = await jobsApiFetch(
            "/search/semantic?q=" +
              encodeURIComponent(this.query) +
              "&limit=20"
          );
          if (!resp.ok) throw new Error("Search failed");
          const data = await resp.json();
          this.semanticResults = data.results || [];
        } catch (e) {
          this.semanticError = "Semantic search unavailable. Try keyword search.";
        } finally {
          this.semanticLoading = false;
        }
      },

      switchMode(newMode) {
        this.mode = newMode;
        if (newMode === "semantic" && this.query) {
          this.searchSemantic();
        }
      },

      formatSalary(job) {
        if (!job.salary_min || job.salary_min === 0) return "";
        const fmt = new Intl.NumberFormat("en-US", {
          style: "currency",
          currency: job.currency || "USD",
          maximumFractionDigits: 0,
        });
        return fmt.format(job.salary_min) + "\u2013" + fmt.format(job.salary_max);
      },
    };
  };
});
