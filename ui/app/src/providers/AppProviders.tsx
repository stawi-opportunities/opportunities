import { useEffect, type ReactNode } from "react";
import { QueryProvider } from "./QueryProvider";
import { AuthProvider } from "./AuthProvider";
import { ToastProvider } from "./ToastProvider";
import { I18nProvider } from "@/i18n/I18nProvider";
import { initPostHog } from "@/analytics/posthog";
import { useEffect, type ReactNode } from 'react';
import { QueryProvider } from './QueryProvider';
import { AuthProvider } from './AuthProvider';
import { I18nProvider } from '@/i18n/I18nProvider';
import { initPostHog } from '@/analytics/posthog';

// Fire the analytics init exactly once per page load — not per React
// island. Every Hugo page mounts its own React root through
// AppProviders, and without this guard we'd double-init, which
// produces duplicate session signals.
let analyticsInitCalled = false;
function ensureAnalyticsInitOnce() {
  if (analyticsInitCalled) return;
  analyticsInitCalled = true;
  initPostHog();
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
    ensureAnalyticsInitOnce();
  }, []);

  return (
    <AuthProvider>
      <QueryProvider>
        <ToastProvider>
          <I18nProvider>{children}</I18nProvider>
        </ToastProvider>
      </QueryProvider>
    </AuthProvider>
  );
}
