import { useEffect, type ReactNode } from "react";
import { QueryProvider } from "./QueryProvider";
import { AuthProvider } from "./AuthProvider";
import { I18nProvider } from "@/i18n/I18nProvider";
import { initOpenObserve } from "@/analytics/openobserve";

// Fire the RUM/logs init exactly once per page load — not per React
// island. Every Hugo page mounts its own React root through
// AppProviders, and without this guard we'd double-init the SDK, which
// produces duplicate sessions and inflates billable events.
let rumInitCalled = false;
function ensureRumInitOnce() {
  if (rumInitCalled) return;
  rumInitCalled = true;
  initOpenObserve();
}

/**
 * Top-level provider tree rendered around every React island.
 * Order matters: auth is the root so react-query hooks can call
 * getAccessToken() without additional wiring. I18nProvider is innermost
 * so components everywhere can call useI18n() without extra wiring.
 */
export function AppProviders({ children }: { children: ReactNode }) {
  // Run once on first mount. Inside an effect so SSR/static HTML
  // generation (Hugo + prerender paths) doesn't touch window.
  useEffect(() => {
    ensureRumInitOnce();
  }, []);

  return (
    <AuthProvider>
      <QueryProvider>
        <I18nProvider>{children}</I18nProvider>
      </QueryProvider>
    </AuthProvider>
  );
}
