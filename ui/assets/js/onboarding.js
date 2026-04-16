document.addEventListener("alpine:init", () => {
  window.onboardingWizard = function () {
    return {
      step: 1,
      loading: false,
      error: "",

      // Step 1 fields
      cvFile: null,
      cvFileName: "",
      targetJobTitle: "",
      experienceLevel: "",
      jobSearchStatus: "",
      salaryRange: "",
      wantsATSReport: false,

      // Step 2 fields
      preferredRegions: [],
      preferredTimezones: [],
      country: "",
      usWorkAuth: null,
      needsSponsorship: null,

      // Step 3
      paymentMethod: "card",
      agreeTerms: false,

      get userName() {
        return Alpine.store("auth").name || "there";
      },

      get canProceedStep1() {
        return this.targetJobTitle && this.experienceLevel && this.jobSearchStatus;
      },

      get canProceedStep2() {
        return this.preferredRegions.length > 0 && this.country;
      },

      nextStep() {
        if (this.step < 3) this.step++;
        this.$nextTick(() => {
          document.getElementById("wizard-top")?.scrollIntoView({ behavior: "smooth" });
        });
      },

      prevStep() {
        if (this.step > 1) this.step--;
      },

      handleFileSelect(event) {
        const file = event.target.files?.[0];
        if (file) {
          if (file.size > 10 * 1024 * 1024) {
            this.error = "File must be under 10MB.";
            return;
          }
          this.cvFile = file;
          this.cvFileName = file.name;
          this.error = "";
        }
      },

      handleFileDrop(event) {
        event.preventDefault();
        const file = event.dataTransfer?.files?.[0];
        if (file) {
          if (file.size > 10 * 1024 * 1024) {
            this.error = "File must be under 10MB.";
            return;
          }
          this.cvFile = file;
          this.cvFileName = file.name;
          this.error = "";
        }
      },

      toggleRegion(region) {
        const idx = this.preferredRegions.indexOf(region);
        if (idx >= 0) {
          this.preferredRegions.splice(idx, 1);
        } else {
          this.preferredRegions.push(region);
        }
      },

      toggleTimezone(tz) {
        const idx = this.preferredTimezones.indexOf(tz);
        if (idx >= 0) {
          this.preferredTimezones.splice(idx, 1);
        } else {
          this.preferredTimezones.push(tz);
        }
      },

      async submitOnboarding() {
        this.loading = true;
        this.error = "";

        try {
          const formData = new FormData();
          if (this.cvFile) formData.append("cv", this.cvFile);
          formData.append("target_job_title", this.targetJobTitle);
          formData.append("experience_level", this.experienceLevel);
          formData.append("job_search_status", this.jobSearchStatus);
          formData.append("preferred_regions", this.preferredRegions.join(", "));
          formData.append("preferred_timezones", this.preferredTimezones.join(", "));
          formData.append("wants_ats_report", String(this.wantsATSReport));
          if (this.usWorkAuth !== null) formData.append("us_work_auth", String(this.usWorkAuth));
          if (this.needsSponsorship !== null) formData.append("needs_sponsorship", String(this.needsSponsorship));

          const salaryMap = {
            "30k-50k": [30000, 50000],
            "50k-75k": [50000, 75000],
            "75k-100k": [75000, 100000],
            "100k+": [100000, 200000],
          };
          const [min, max] = salaryMap[this.salaryRange] || [0, 0];
          formData.append("salary_min", String(min));
          formData.append("salary_max", String(max));
          formData.append("currency", "USD");

          const resp = await apiFetch("/candidates/onboard", {
            method: "POST",
            body: formData,
          });

          if (!resp.ok) {
            const data = await resp.json().catch(() => ({}));
            throw new Error(data.error || "Registration failed");
          }

          await Alpine.store("auth").fetchProfile();

          if (this.agreeTerms && this.paymentMethod) {
            await this.initiatePayment();
          } else {
            window.location.href = "/dashboard/";
          }
        } catch (e) {
          this.error = e.message;
        } finally {
          this.loading = false;
        }
      },

      async initiatePayment() {
        try {
          const endpoint =
            this.paymentMethod === "mobile" ? "/billing/mobile-pay" : "/billing/checkout";
          const resp = await apiFetch(endpoint, {
            method: "POST",
            body: JSON.stringify({ plan_id: "premium_monthly" }),
          });

          if (!resp.ok) throw new Error("Payment initiation failed");

          const data = await resp.json();
          if (data.checkout_url) {
            window.location.href = data.checkout_url;
          } else {
            window.location.href = "/dashboard/?payment=pending";
          }
        } catch (e) {
          window.location.href = "/dashboard/?payment=failed";
        }
      },

      skipPayment() {
        this.submitOnboarding().then(() => {
          window.location.href = "/dashboard/";
        });
      },
    };
  };
});
