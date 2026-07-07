import { createAuthRuntime, type AuthRuntime } from "@stawi/auth-runtime";
import { getConfig } from "@/utils/config";

// Module-level singleton — same pattern as ui/app/src/auth/runtime.ts.
// One auth runtime across the SPA so React components and any
// non-React helpers share token + role state.
let instance: AuthRuntime | null = null;

export const opportunitiesAdminAuthScopes = [
  "openid",
  "profile",
  "offline_access",
] as const;

export function authRuntime(): AuthRuntime {
  if (instance) return instance;
  const cfg = getConfig();
  instance = createAuthRuntime({
    clientId: cfg.oidcClientID,
    installationId: cfg.oidcInstallationID,
    idpBaseUrl: cfg.oidcIssuer,
    // Admin /admin/trace/* is served by the
    // api service at the bare api.stawi.org root (alongside /jobs/*).
    // candidatesAPIURL is the bare root by convention; the matching
    // service's /matching/* prefix is added inline by call sites.
    apiBaseUrl: cfg.candidatesAPIURL,
    redirectUri: cfg.oidcRedirectURI,
    scopes: [...opportunitiesAdminAuthScopes],
    skipFedCM: true,
  });
  return instance;
}

export async function getRoles(): Promise<string[]> {
  return authRuntime().getRoles();
}

export async function fetchAdminJSON<T = unknown>(path: string): Promise<T> {
  return authRuntime().fetch<T>(path);
}

// =====================================================================
// Response shapes — mirror the JSON returned by apps/api/cmd/*_admin.go.
// =====================================================================

export type SourceTraceResponse = {
  source: {
    id: string;
    type: string;
    base_url: string;
    country: string;
    status: string;
    health_score: number;
    next_crawl_at: string | null;
    last_seen_at: string | null;
  };
  summary: {
    window: string;
    crawl_jobs: number;
    crawl_jobs_failed: number;
    variants_emitted: number;
    variants_published: number;
    variants_rejected: number;
    rejection_reasons: Record<string, number>;
    data_source?: string;
  };
  recent_crawls: Array<{
    crawl_job_id: string;
    scheduled_at: string;
    started_at: string | null;
    finished_at: string | null;
    duration_ms: number;
    status: string;
    jobs_found: number;
    jobs_stored: number;
    error_code?: string;
  }>;
};

// VariantTimelineResponse mirrors pkg/repository.VariantTimeline JSON
// shape served by GET /admin/trace/variants/{id}.
export type VariantTimelineResponse = {
  variant_id: string;
  external_id?: string;
  hard_key?: string;
  source: { id: string; type: string };
  crawl_job?: {
    crawl_job_id: string;
    scheduled_at: string;
    started_at?: string;
    finished_at?: string;
    duration_ms: number;
    status: string;
    jobs_found: number;
    jobs_stored: number;
    error_code?: string;
  };
  stages: Array<{
    stage: string;
    at: string;
    duration_ms?: number;
    canonical_id?: string;
  }>;
  current_stage: string;
  opportunity_slug?: string;
  last_error?: string;
};

export type OpportunityTraceResponse = {
  slug: string;
  variant_count: number;
  variants: Array<{
    variant_id: string;
    source: { id: string; type: string };
    ingested_at: string;
    joined_at: string;
  }>;
};

export type SeedDigestResponse = {
  source_id: string;
  date: string;
  data_source: "postgres";
  crawl_jobs?: number;
  variants_emitted: number;
  variants_rejected: number;
  variants_published: number;
  rejection_reasons: Record<string, number>;
};

export type DefinitionEntry = {
  type: string;
  name: string;
  version: string;
  updated_at: string;
  size: number;
};

// /admin/definitions (no ?type=) returns a map keyed by type name.
// /admin/definitions?type=X returns {type, items: [...]} — see
// listDefinitionsByType for that shape.
export type DefinitionsListResponse = Record<string, DefinitionEntry[]>;

export type SourceListItem = {
  id: string;
  type: string;
  name?: string;
  base_url?: string;
  status: string;
  country: string;
  health_score: number;
  // Adaptive recrawl (Plan D3). Score is 0.0–1.0, 1.0 = crawl at
  // min_interval. next_crawl_at is the scheduler's derived target.
  // Optional because older API responses (pre-D3) may not include
  // them; the UI falls back to existing fixed-interval columns.
  score?: number;
  next_crawl_at?: string;
  min_interval_minutes?: number;
  max_interval_minutes?: number;
};

// Response shape for POST /admin/sources/{id}/rescore. The handler
// recomputes the freshness score from the latest crawl_signals view
// and persists the derived next_crawl_at.
export type RescoreResponse = {
  ok: boolean;
  id: string;
  score: number;
  next_crawl_at: string;
};

// Response wrapper for GET /admin/sources — the handler returns
// {sources, total, limit, offset}.
export type SourceListResponse = {
  sources: SourceListItem[];
  total: number;
  limit: number;
  offset: number;
};

// =====================================================================
// Fetch wrappers — one per endpoint.
// =====================================================================

export const getSourceTrace = (id: string, since: string = "24h") =>
  fetchAdminJSON<SourceTraceResponse>(
    `/admin/trace/sources/${encodeURIComponent(id)}?since=${encodeURIComponent(since)}`,
  );

export const getVariantTrace = (id: string) =>
  fetchAdminJSON<VariantTimelineResponse>(
    `/admin/trace/variants/${encodeURIComponent(id)}`,
  );

export const getOpportunityTrace = (slug: string) =>
  fetchAdminJSON<OpportunityTraceResponse>(
    `/admin/trace/opportunities/${encodeURIComponent(slug)}`,
  );

export const getSeedDigest = (id: string, date: string) =>
  fetchAdminJSON<SeedDigestResponse>(
    `/admin/trace/seeds/${encodeURIComponent(id)}/digest?date=${encodeURIComponent(date)}`,
  );

export const listDefinitions = (type?: string) => {
  const query = type ? `?type=${encodeURIComponent(type)}` : "";
  return fetchAdminJSON<DefinitionsListResponse>(`/admin/definitions${query}`);
};

// getDefinition fetches the raw YAML body of a single definition. The
// admin-runtime's `fetch<T>` returns whatever `parse()` produces: for
// `application/json` responses it JSON.parse-es the body; for anything
// else (which the definitions GET serves as `text/yaml` or
// `application/x-yaml`) it returns the decoded text as a string. So
// typing the call as `<string>` is correct.
export const getDefinition = (type: string, name: string): Promise<string> =>
  authRuntime().fetch<string>(
    `/admin/definitions/${encodeURIComponent(type)}/${encodeURIComponent(name)}`,
    { headers: { Accept: "application/x-yaml" } },
  );

export const putDefinition = async (
  type: string,
  name: string,
  body: string,
): Promise<void> => {
  await authRuntime().fetch(
    `/admin/definitions/${encodeURIComponent(type)}/${encodeURIComponent(name)}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/x-yaml" },
      body,
    },
  );
};

export const deleteDefinition = async (
  type: string,
  name: string,
): Promise<void> => {
  await authRuntime().fetch(
    `/admin/definitions/${encodeURIComponent(type)}/${encodeURIComponent(name)}`,
    { method: "DELETE" },
  );
};

export const listSources = async (
  limit: number = 100,
  offset: number = 0,
): Promise<SourceListResponse> => {
  return fetchAdminJSON<SourceListResponse>(
    `/admin/sources?limit=${limit}&offset=${offset}`,
  );
};

// rescoreSource force-recomputes a source's freshness score +
// next_crawl_at from the latest crawl_signals view. Useful when an
// operator changes a source's min/max interval and wants the new
// bounds applied immediately rather than waiting for the next
// scheduler tick.
export const rescoreSource = (id: string): Promise<RescoreResponse> =>
  authRuntime().fetch<RescoreResponse>(
    `/admin/sources/${encodeURIComponent(id)}/rescore`,
    { method: "POST" },
  );

// Per-source iterator checkpoint shape returned by GET /admin/checkpoints.
// Mirrors pkg/repository.Checkpoint — cursor is the connector's own
// JSON shape, opaque to the UI.
export type CheckpointRow = {
  source_id: string;
  connector_type: string;
  cursor: unknown;
  page_idx: number;
  last_url?: string;
  last_checkpoint_at: string;
};

export type CheckpointListResponse = {
  checkpoints: CheckpointRow[];
  count: number;
};

export const listCheckpoints = (
  sourceID?: string,
): Promise<CheckpointListResponse> => {
  const q = sourceID ? `?source_id=${encodeURIComponent(sourceID)}` : "";
  return fetchAdminJSON<CheckpointListResponse>(`/admin/checkpoints${q}`);
};

// clearCheckpoint deletes one (source_id, connector_type) row. Idempotent
// on the server — clearing an already-missing checkpoint returns 204.
export const clearCheckpoint = async (
  sourceID: string,
  connectorType: string,
): Promise<void> => {
  await authRuntime().fetch(
    `/admin/checkpoints/${encodeURIComponent(sourceID)}/${encodeURIComponent(connectorType)}`,
    { method: "DELETE" },
  );
};
