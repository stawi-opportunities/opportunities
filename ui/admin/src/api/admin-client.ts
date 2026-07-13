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

/**
 * Crawl engines only. Site-specific boards are data: source row + recipe.
 * Legacy type strings still appear on old rows but new sources use these.
 */
export const SOURCE_TYPES = [
  "api",
  "schema_org",
  "sitemap",
  "generic_html",
  "workday",
  "smartrecruiters_api",
] as const;

/** Bundled stock recipes (definitions/stock-recipes) for common public APIs. */
export const STOCK_RECIPES = [
  "remoteok",
  "arbeitnow",
  "jobicy",
  "themuse",
  "himalayas",
] as const;

export type SourceType = (typeof SOURCE_TYPES)[number];

export const SOURCE_STATUSES = [
  "pending",
  "verifying",
  "verified",
  "rejected",
  "active",
  "degraded",
  "paused",
  "blocked",
  "disabled",
] as const;

export type SourceListItem = {
  id: string;
  type: string;
  name?: string;
  base_url?: string;
  status: string;
  country: string;
  language?: string;
  health_score: number;
  kinds?: string[];
  listing_path?: string;
  crawl_interval_sec?: number;
  needs_tuning?: boolean;
  frontier_enabled?: boolean;
  auto_approve?: boolean;
  extraction_recipe?: string;
  // Adaptive recrawl (Plan D3). Score is 0.0–1.0, 1.0 = crawl at
  // min_interval. next_crawl_at is the scheduler's derived target.
  score?: number;
  next_crawl_at?: string;
  min_interval_minutes?: number;
  max_interval_minutes?: number;
  verification_report?: VerificationReport;
  rejection_reason?: string;
};

export type VerificationReport = {
  started_at?: string;
  completed_at?: string;
  url_valid?: boolean;
  blocklist_clean?: boolean;
  kinds_known?: boolean;
  reachable?: boolean;
  reachable_status?: number;
  robots_allowed?: boolean;
  sample_extracted?: boolean;
  sample_verify_pass?: boolean;
  sample_reasons?: string[];
  sample_title?: string;
  overall_pass?: boolean;
  errors?: string[];
};

/** Full source row from GET /admin/sources/{id}. */
export type AdminSource = SourceListItem & {
  priority?: number;
  config?: string;
  extraction_prompt_extension?: string;
  required_attributes_by_kind?: Record<string, string[]>;
  last_seen_at?: string;
  approved_at?: string;
  approved_by?: string;
  last_stopped_at?: string;
  last_stopped_by?: string;
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

export type ListSourcesParams = {
  limit?: number;
  offset?: number;
  status?: string;
  type?: string;
  country?: string;
  kind?: string;
};

export const listSources = async (
  params: ListSourcesParams | number = 100,
  offset: number = 0,
): Promise<SourceListResponse> => {
  // Back-compat: listSources(limit, offset)
  const p: ListSourcesParams =
    typeof params === "number"
      ? { limit: params, offset }
      : { limit: 100, offset: 0, ...params };
  const qs = new URLSearchParams();
  qs.set("limit", String(p.limit ?? 100));
  qs.set("offset", String(p.offset ?? 0));
  if (p.status) qs.set("status", p.status);
  if (p.type) qs.set("type", p.type);
  if (p.country) qs.set("country", p.country);
  if (p.kind) qs.set("kind", p.kind);
  return fetchAdminJSON<SourceListResponse>(`/admin/sources?${qs.toString()}`);
};

export const listDiscoveredSources = (
  limit = 100,
): Promise<{ sources: AdminSource[]; count: number }> =>
  fetchAdminJSON(`/admin/sources/discovered?limit=${limit}`);

export const getSource = (id: string): Promise<AdminSource> =>
  fetchAdminJSON<AdminSource>(`/admin/sources/${encodeURIComponent(id)}`);

export type CreateSourceRequest = {
  type: string;
  base_url: string;
  name?: string;
  country?: string;
  language?: string;
  kinds?: string[];
  crawl_interval_sec?: number;
  listing_path?: string;
  auto_approve?: boolean;
  priority?: number;
  /** Stock recipe name (definitions/stock-recipes/{name}.json). */
  recipe?: string;
};

export const createSource = (body: CreateSourceRequest): Promise<AdminSource> =>
  authRuntime().fetch<AdminSource>(`/admin/sources`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

export type UpdateSourceRequest = {
  name?: string;
  country?: string;
  language?: string;
  priority?: number;
  crawl_interval_sec?: number;
  kinds?: string[];
  listing_path?: string;
  auto_approve?: boolean;
  extraction_prompt_extension?: string;
  frontier_enabled?: boolean;
};

export const updateSource = (
  id: string,
  body: UpdateSourceRequest,
): Promise<AdminSource> =>
  authRuntime().fetch<AdminSource>(`/admin/sources/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

export const deleteSource = (
  id: string,
  hard = false,
): Promise<{ ok: boolean; id: string; hard: boolean }> =>
  authRuntime().fetch(
    `/admin/sources/${encodeURIComponent(id)}${hard ? "?hard=true" : ""}`,
    { method: "DELETE" },
  );

// rescoreSource force-recomputes a source's freshness score +
// next_crawl_at from the latest crawl_signals view.
export const rescoreSource = (id: string): Promise<RescoreResponse> =>
  authRuntime().fetch<RescoreResponse>(
    `/admin/sources/${encodeURIComponent(id)}/rescore`,
    { method: "POST" },
  );

export type SourceActionResponse = {
  ok: boolean;
  id?: string;
  status?: string;
  dispatched?: number;
  reason?: string;
  source_id?: string;
};

export const crawlSource = (id: string): Promise<SourceActionResponse> =>
  authRuntime().fetch<SourceActionResponse>(
    `/admin/sources/${encodeURIComponent(id)}/crawl`,
    { method: "POST" },
  );

export const pauseSource = (id: string): Promise<SourceActionResponse> =>
  authRuntime().fetch<SourceActionResponse>(
    `/admin/sources/${encodeURIComponent(id)}/pause`,
    { method: "POST" },
  );

export const resumeSource = (id: string): Promise<SourceActionResponse> =>
  authRuntime().fetch<SourceActionResponse>(
    `/admin/sources/${encodeURIComponent(id)}/resume`,
    { method: "POST" },
  );

export const stopSource = (id: string): Promise<SourceActionResponse> =>
  authRuntime().fetch<SourceActionResponse>(
    `/admin/sources/${encodeURIComponent(id)}/stop`,
    { method: "POST" },
  );

export const startSource = (id: string): Promise<SourceActionResponse> =>
  authRuntime().fetch<SourceActionResponse>(
    `/admin/sources/${encodeURIComponent(id)}/start`,
    { method: "POST" },
  );

export const verifySource = (id: string): Promise<VerificationReport> =>
  authRuntime().fetch<VerificationReport>(
    `/admin/sources/${encodeURIComponent(id)}/verify`,
    { method: "POST" },
  );

export const approveSource = (id: string): Promise<SourceActionResponse> =>
  authRuntime().fetch<SourceActionResponse>(
    `/admin/sources/${encodeURIComponent(id)}/approve`,
    { method: "POST" },
  );

export const rejectSource = (
  id: string,
  reason?: string,
): Promise<SourceActionResponse> =>
  authRuntime().fetch<SourceActionResponse>(
    `/admin/sources/${encodeURIComponent(id)}/reject`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ reason: reason || "rejected via admin" }),
    },
  );

// ── Recipes ─────────────────────────────────────────────────────────

export type ActiveRecipeResponse = {
  source_id: string;
  has_recipe: boolean;
  recipe: Record<string, unknown> | null;
  listing_path?: string;
  needs_tuning?: boolean;
};

export type RecipeHistoryRow = {
  id: string;
  version: number;
  status: string;
  pass_rate: number;
  model: string;
  validation_report?: unknown;
  recipe: unknown;
  created_at: string;
};

export type RecipeTestReport = {
  source_id: string;
  mode?: string;
  acquisition?: string;
  threshold?: number;
  structural?: string;
  verified?: boolean;
  pass_rate?: number;
  gate?: string;
  page?: {
    http_status?: number;
    item_count?: number;
    more_pages?: boolean;
    verify_passed?: number;
    error?: string;
    items?: Array<{
      title?: string;
      issuing_entity?: string;
      apply_url?: string;
      kind?: string;
      verify_ok?: boolean;
      missing?: string[];
      hard_key?: string;
    }>;
  };
  validation?: {
    samples: number;
    passed: number;
    pass_rate: number;
    per_sample?: Array<{
      url: string;
      ok: boolean;
      missing?: string[];
      error?: string;
    }>;
  };
  found?: number;
  accepted?: number;
  rejected?: number;
  items?: Array<Record<string, unknown>>;
  rejections?: Array<Record<string, unknown>>;
  message?: string;
};

export const getActiveRecipe = (id: string): Promise<ActiveRecipeResponse> =>
  fetchAdminJSON(`/admin/sources/${encodeURIComponent(id)}/recipe`);

export const listRecipeHistory = (
  id: string,
): Promise<{ source_id: string; versions: RecipeHistoryRow[]; count: number }> =>
  fetchAdminJSON(`/admin/sources/${encodeURIComponent(id)}/recipes`);

export const putRecipe = (
  id: string,
  recipe: unknown,
  activate = true,
): Promise<{ ok: boolean; activated: boolean; recipe?: unknown }> =>
  authRuntime().fetch(`/admin/sources/${encodeURIComponent(id)}/recipe`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ recipe, activate }),
  });

export const testRecipe = (
  id: string,
  recipe?: unknown,
  samples = 3,
): Promise<RecipeTestReport> =>
  authRuntime().fetch(`/admin/sources/${encodeURIComponent(id)}/recipe/test`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(recipe ? { recipe, samples } : { samples }),
  });

export const generateRecipe = (
  id: string,
  sampleURLs?: string[],
): Promise<{ ok: boolean; queued: boolean; message?: string }> =>
  authRuntime().fetch(
    `/admin/sources/${encodeURIComponent(id)}/recipe/generate`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        sample_urls: sampleURLs,
        reason: "admin_manual",
      }),
    },
  );

export const rollbackRecipe = (
  id: string,
  version: number,
): Promise<{ ok: boolean; version: number; recipe?: unknown }> =>
  authRuntime().fetch(
    `/admin/sources/${encodeURIComponent(id)}/recipe/rollback`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ version }),
    },
  );

/** Dry-run extract without enqueueing (recipe if present, else connector). */
export const testSourceExtract = (id: string): Promise<RecipeTestReport> =>
  authRuntime().fetch(`/admin/sources/${encodeURIComponent(id)}/test`, {
    method: "POST",
  });

// ── Crawl runs (resumable slice state machine) ──────────────────────

export type CrawlRunRow = {
  id: string;
  source_id: string;
  status: string;
  cursor?: unknown;
  lease_expires_at?: string;
  started_at?: string;
  updated_at?: string;
  finished_at?: string;
  error_code?: string;
  error_message?: string;
  jobs_found?: number;
  jobs_stored?: number;
  jobs_rejected?: number;
};

export type CrawlRunListResponse = {
  runs: CrawlRunRow[];
  count: number;
};

export const listCrawlRuns = (
  sourceID?: string,
  limit = 50,
): Promise<CrawlRunListResponse> => {
  const params = new URLSearchParams();
  if (sourceID) params.set("source_id", sourceID);
  params.set("limit", String(limit));
  return fetchAdminJSON<CrawlRunListResponse>(
    `/admin/crawl-runs?${params.toString()}`,
  );
};

export const resetCrawlRun = (
  sourceID: string,
): Promise<{ reset: boolean; run_id?: string; reason?: string }> =>
  authRuntime().fetch(`/admin/crawl-runs/${encodeURIComponent(sourceID)}/reset`, {
    method: "POST",
  });

// ── Opportunities (admin browse + sanitize) ─────────────────────────

export type AdminOpportunity = {
  canonical_id: string;
  slug: string;
  kind: string;
  source_id?: string;
  title: string;
  description?: string;
  issuing_entity?: string;
  country?: string;
  region?: string;
  city?: string;
  remote: boolean;
  apply_url: string;
  posted_at?: string;
  deadline?: string;
  currency?: string;
  amount_min?: number;
  amount_max?: number;
  employment_type?: string;
  seniority?: string;
  geo_scope?: string;
  status: string;
  hidden: boolean;
  hidden_reason?: string;
  first_seen_at: string;
  last_seen_at: string;
  attributes?: Record<string, unknown>;
  updated_at: string;
};

export type AdminOpportunityListResponse = {
  opportunities: AdminOpportunity[];
  total: number;
  limit: number;
  offset: number;
};

export type ListOpportunitiesParams = {
  q?: string;
  kind?: string;
  country?: string;
  source_id?: string;
  hidden?: "true" | "false" | "";
  limit?: number;
  offset?: number;
};

export const listOpportunities = (
  params: ListOpportunitiesParams = {},
): Promise<AdminOpportunityListResponse> => {
  const qs = new URLSearchParams();
  if (params.q) qs.set("q", params.q);
  if (params.kind) qs.set("kind", params.kind);
  if (params.country) qs.set("country", params.country);
  if (params.source_id) qs.set("source_id", params.source_id);
  if (params.hidden) qs.set("hidden", params.hidden);
  qs.set("limit", String(params.limit ?? 50));
  qs.set("offset", String(params.offset ?? 0));
  return fetchAdminJSON<AdminOpportunityListResponse>(
    `/admin/opportunities?${qs.toString()}`,
  );
};

export const getOpportunity = (slug: string): Promise<AdminOpportunity> =>
  fetchAdminJSON<AdminOpportunity>(
    `/admin/opportunities/${encodeURIComponent(slug)}`,
  );

export type OpportunityPatch = {
  title?: string;
  description?: string;
  issuing_entity?: string;
  apply_url?: string;
  country?: string;
  region?: string;
  city?: string;
  clear_fields?: string[];
  clear_attributes?: string[];
  set_attributes?: Record<string, unknown>;
  hide?: boolean;
  hidden_reason?: string;
};

export const patchOpportunity = (
  slug: string,
  body: OpportunityPatch,
): Promise<AdminOpportunity> =>
  authRuntime().fetch<AdminOpportunity>(
    `/admin/opportunities/${encodeURIComponent(slug)}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
  );

export const hideOpportunity = (
  slug: string,
  reason?: string,
): Promise<{ ok: boolean; slug: string; hidden: boolean }> => {
  const q = reason ? `?reason=${encodeURIComponent(reason)}` : "";
  return authRuntime().fetch(
    `/admin/opportunities/${encodeURIComponent(slug)}${q}`,
    { method: "DELETE" },
  );
};

export const unhideOpportunity = (
  slug: string,
): Promise<{ ok: boolean; slug: string; hidden: boolean }> =>
  authRuntime().fetch(
    `/admin/opportunities/${encodeURIComponent(slug)}/unhide`,
    { method: "POST" },
  );

// ── Ops overview + rejections ───────────────────────────────────────

export type OpsOverview = {
  counts: {
    active_jobs: number;
    hidden_jobs: number;
    sources_active: number;
    sources_paused: number;
    sources_total: number;
    queue_pending: number;
    queue_processing: number;
    queue_dead: number;
    rejected_24h: number;
    published_24h: number;
    crawl_jobs_24h: number;
    crawl_failed_24h: number;
    active_runs: number;
  };
  rejection_reasons: Record<string, number>;
  recent: AdminOpportunity[];
  generated_at: string;
};

export const getOpsOverview = (): Promise<OpsOverview> =>
  fetchAdminJSON<OpsOverview>(`/admin/ops/overview`);

export type RejectionRow = {
  variant_id: string;
  source_id: string;
  occurred_at: string;
  details: Record<string, unknown> | string;
};

export type RejectionListResponse = {
  rejections: RejectionRow[];
  count: number;
};

export const listRejections = (limit = 100): Promise<RejectionListResponse> =>
  fetchAdminJSON<RejectionListResponse>(
    `/admin/variants/rejected?limit=${limit}`,
  );
