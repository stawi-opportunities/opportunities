import type { ReactNode } from "react";
import { QueryProvider } from "./QueryProvider";
import { AuthProvider } from "./AuthProvider";

/**
 * Top-level provider tree rendered around every React island.
 * Order matters: auth is the root so react-query hooks can call
 * getAccessToken() without additional wiring.
 */
export function AppProviders({ children }: { children: ReactNode }) {
  return (
    <AuthProvider>
      <QueryProvider>{children}</QueryProvider>
    </AuthProvider>
  );
}
