import { jobsApiGet, type ApiParams } from "./client";
import type {
  SearchResponse,
  SearchParams,
  LatestJobsResponse,
  StatsSummary,
  CategoryListResponse,
  CategoryJobsResponse,
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
