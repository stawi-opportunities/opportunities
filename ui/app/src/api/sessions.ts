// API client for the session-capture endpoints on the matching service.
// All authed calls go through @stawi/auth-runtime (`runtime.fetch`),
// which attaches the OIDC JWT — the gateway verifies the signature and
// the matching service trusts the sub claim.

import { authRuntime } from '@/auth/runtime';

export interface SourceAuthManifest {
  source_type: string;
  auth_method: 'none' | 'extension' | 'oauth' | 'api_token';
  display_name: string;
  login_url: string;
  home_url?: string;
  instructions_md: string;
}

export interface ConnectedSession {
  source_type: string;
  status: 'connected' | 'expired' | 'revoked';
  captured_at: string;
  expires_at?: string;
  last_used_at?: string;
  user_agent?: string;
  capture_origin?: string;
}

export interface PairingCreateResponse {
  code: string;
  expires_in: number;
}

/** GET /sources/:source_type/auth — manifest + instructions for one source. */
export async function fetchSourceAuth(sourceType: string): Promise<SourceAuthManifest> {
  return authRuntime().fetch(`/sources/${encodeURIComponent(sourceType)}/auth`);
}

/** GET /candidates/me/sessions — per-source connection status for the candidate. */
export async function fetchSessions(): Promise<ConnectedSession[]> {
  const body = await authRuntime().fetch<{ sessions: ConnectedSession[] }>(
    '/candidates/me/sessions'
  );
  return body.sessions ?? [];
}

/** POST /pairings — mint a 6-character pairing code the user types into the extension. */
export async function createPairing(): Promise<PairingCreateResponse> {
  return authRuntime().fetch('/pairings', { method: 'POST' });
}

/** POST /pairings/revoke — drop every Stawi token issued for this candidate. */
export async function revokeExtension(): Promise<void> {
  await authRuntime().fetch('/pairings/revoke', { method: 'POST' });
}

/** DELETE /candidates/me/sessions/:source_type — revoke one source's session. */
export async function revokeSession(sourceType: string): Promise<void> {
  await authRuntime().fetch(`/candidates/me/sessions/${encodeURIComponent(sourceType)}`, {
    method: 'DELETE',
  });
}

/** Fetch the full manifest list + per-source connection status, joined for rendering. */
export async function fetchConnectedAccounts(): Promise<
  Array<SourceAuthManifest & { session?: ConnectedSession }>
> {
  const list = await authRuntime().fetch<{ sources: SourceAuthManifest[] }>(
    '/sources/auth-manifest'
  );
  const sessions = await fetchSessions();
  const byType = new Map(sessions.map((s) => [s.source_type, s]));
  return (list.sources ?? []).map((src) => ({
    ...src,
    session: byType.get(src.source_type),
  }));
}
