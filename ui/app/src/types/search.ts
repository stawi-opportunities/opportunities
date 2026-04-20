// Mirrors pkg/repository/job.go SearchResult and pkg/repository/facets.go.

export interface SearchResult {
  id: string;
  slug: string;
  title: string;
  company: string;
  location_text: string;
  country: string;
  remote_type: string;
  category: string;
  salary_min: number;
  salary_max: number;
  currency: string;
  posted_at: string | null;
  quality_score: number;
  snippet: string;
  is_featured: boolean;
}

export interface FacetEntry {
  key: string;
  count: number;
}

export interface Facets {
  category: FacetEntry[];
  remote_type: FacetEntry[];
  employment_type: FacetEntry[];
  seniority: FacetEntry[];
  country: FacetEntry[];
}

export interface SearchResponse {
  query: string;
  results: SearchResult[];
  has_more: boolean;
  cursor_next: string;
  facets: Facets;
  sort: "relevance" | "recent" | "quality" | "salary_high";
}

export interface SearchParams {
  q?: string;
  category?: string;
  remote_type?: string;
  employment_type?: string;
  seniority?: string;
  country?: string;
  sort?: "relevance" | "recent" | "quality" | "salary_high";
  limit?: number;
  offset?: number;
  cursor?: string;
}

export interface LatestJobsResponse {
  results: SearchResult[];
}

export interface StatsSummary {
  total_jobs: number;
  total_companies: number;
  active_sources: number;
}

export interface CategoryListResponse {
  categories: FacetEntry[];
}

export interface CategoryJobsResponse {
  category: string;
  results: SearchResult[];
  has_more: boolean;
  cursor_next: string;
}

// Mirrors apps/api/cmd/tiered.go:tieredResponseTier.
export type FeedTierID = "preferred" | "local" | "regional" | "global";

export interface FeedTier {
  id: FeedTierID;
  label: string;
  jobs: SearchResult[];
  cursor: string;
  has_more: boolean;
  /** Echoes the filter scope used by the server so the "Load more"
   *  request targets exactly the same slice of the catalog. */
  country?: string;
  countries?: string[];
  language?: string;
}

export interface FeedContext {
  country: string;
  languages: string[];
  region: string;
}

export interface FeedResponse {
  context: FeedContext;
  tiers: FeedTier[];
  facets: Facets;
  sort: "relevance" | "recent" | "quality" | "salary_high";
}

export interface FeedParams extends SearchParams {
  /** Comma-separated ISO-3166 country codes the user has told us
   *  they prefer (CandidateProfile.preferred_countries). */
  boost_countries?: string;
  /** Comma-separated BCP-47 base language tags (en / sw / fr ...). */
  boost_languages?: string;
  /** Overrides CF-IPCountry when supplied (debug / preview). */
  country?: string;
  /** Comma-separated base language tags; overrides Accept-Language. */
  lang?: string;
  /** Max jobs per tier (default 25). */
  tier_limit?: number;
}

export interface TierPageResponse {
  jobs: SearchResult[];
  cursor_next: string;
  has_more: boolean;
}

export interface TierPageParams extends SearchParams {
  country?: string;
  countries?: string;          // comma-separated
  language?: string;
  exclude_countries?: string;  // comma-separated
}
