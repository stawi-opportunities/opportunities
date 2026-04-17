// Mirrors pkg/repository/job.go SearchResult and pkg/repository/facets.go.

export interface SearchResult {
  id: number;
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
