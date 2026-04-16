// Alpine.js app initialization — placeholder until Task 12
document.addEventListener("alpine:init", () => {
  Alpine.store("auth", {
    isAuthenticated: false,
    name: "",
    initials: "",
    roles: [],
    init() {},
    logout() { window.location.href = "/"; },
  });

  window.appRoot = function () {
    return { mobileMenuOpen: false, init() {} };
  };
});
