import { useEffect, type ReactNode } from "react";
import { QueryProvider } from "./QueryProvider";
import { AuthProvider } from "./AuthProvider";
import { ToastProvider } from "./ToastProvider";
import { I18nProvider } from "@/i18n/I18nProvider";
import { initPostHog } from "@/analytics/posthog";

let analyticsInitCalled = false;
function ensureAnalyticsInitOnce() {
  if (analyticsInitCalled) return;
  analyticsInitCalled = true;
  initPostHog();
}

export function AppProviders({ children }: { children: ReactNode }) {
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