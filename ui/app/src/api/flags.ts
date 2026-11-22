import { authRuntime } from '@/auth/runtime';

// Flag API — POST /opportunities/{slug}/flag (auth required).
// Mirrors the Go domain.FlagReason enum and the userFlagRequest body
// shape in apps/api/cmd/flags_admin.go.

export type FlagReason = 'scam' | 'expired' | 'duplicate' | 'spam' | 'other';

export interface FlagPayload {
  reason: FlagReason;
  description?: string;
}

export interface FlagResponse {
  id: string;
  slug: string;
  kind: string;
  reason: FlagReason;
  auto_actioned: boolean;
}

/**
 * Path of the flag endpoint relative to the auth runtime's base URL
 * (https://api.stawi.org). The edge gateway strips the `/opportunities`
 * route prefix before forwarding to the opportunities api, whose own route
 * is `/opportunities/{slug}/flag` — so the public path carries the prefix
 * twice, exactly like the view/apply beacons under `apiURL`.
 */
export function flagPath(slug: string): string {
  return `/opportunities/opportunities/${encodeURIComponent(slug)}/flag`;
}

/**
 * POST /opportunities/{slug}/flag — auth'd. Throws on non-2xx;
 * callers should handle 409 (already_flagged) and 401/403 (sign in
 * again: missing, expired or wrong-audience token) as distinct UI states.
 */
export async function submitFlag(slug: string, payload: FlagPayload): Promise<FlagResponse> {
  return authRuntime().fetch(flagPath(slug), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      reason: payload.reason,
      description: payload.description?.trim() ?? '',
    }),
  });
}
