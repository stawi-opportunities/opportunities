import type { ReactNode } from "react";
import { QueryProvider } from "./QueryProvider";
import { AuthProvider } from "./AuthProvider";
import { I18nProvider } from "@/i18n/I18nProvider";

/**
 * Top-level provider tree rendered around every React island.
 * Order matters: auth is the root so react-query hooks can call
 * getAccessToken() without additional wiring. I18nProvider is innermost
 * so components everywhere can call useI18n() without extra wiring.
 */
export function AppProviders({ children }: { children: ReactNode }) {
  return (
    <AuthProvider>
      <QueryProvider>
        <I18nProvider>{children}</I18nProvider>
      </QueryProvider>
    </AuthProvider>
  );
}
