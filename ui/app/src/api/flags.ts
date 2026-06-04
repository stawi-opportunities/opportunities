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
 * POST /opportunities/{slug}/flag — auth'd. Throws on non-2xx;
 * callers should handle 409 (already_flagged) and 401 (unauthorized)
 * as distinct UI states.
 */
export async function submitFlag(slug: string, payload: FlagPayload): Promise<FlagResponse> {
  return authRuntime().fetch(`/opportunities/${encodeURIComponent(slug)}/flag`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      reason: payload.reason,
      description: payload.description?.trim() ?? '',
    }),
  });
}
