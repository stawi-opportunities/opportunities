import { jobsApiGet, type ApiParams } from "./client";
import type {
  SearchResponse,
  SearchParams,
  LatestJobsResponse,
  StatsSummary,
  CategoryListResponse,
  CategoryJobsResponse,
  FeedResponse,
  FeedParams,
  TierPageResponse,
  TierPageParams,
} from "@/types/search";

export function searchJobs(params: SearchParams): Promise<SearchResponse> {
  return jobsApiGet<SearchResponse>("/api/search", params as ApiParams);
}

export function latestJobs(limit = 20): Promise<LatestJobsResponse> {
  return jobsApiGet<LatestJobsResponse>("/api/jobs/latest", { limit });
}

export function statsSummary(): Promise<StatsSummary> {
  return jobsApiGet<StatsSummary>("/api/stats/summary");
}

export function listCategories(): Promise<CategoryListResponse> {
  return jobsApiGet<CategoryListResponse>("/api/categories");
}

export function categoryJobs(
  slug: string,
  opts: { cursor?: string; limit?: number } = {},
): Promise<CategoryJobsResponse> {
  return jobsApiGet<CategoryJobsResponse>(
    `/api/categories/${encodeURIComponent(slug)}/jobs`,
    opts,
  );
}

/**
 * Tiered discovery feed — {preferred, local, regional, global}.
 * CF-IPCountry + Accept-Language drive the cascade on anonymous
 * requests; boost_countries / boost_languages override for logged-in
 * users with declared preferences. Pagination of an individual tier
 * uses `loadTierPage` with the echo'd filter scope.
 */
export function feed(params: FeedParams = {}): Promise<FeedResponse> {
  return jobsApiGet<FeedResponse>("/api/feed", params as ApiParams);
}

export function loadTierPage(params: TierPageParams): Promise<TierPageResponse> {
  return jobsApiGet<TierPageResponse>("/api/feed/tier", params as ApiParams);
}
