import { ApiError, jobsApiGet } from './client';
import type { OpportunitySnapshot } from '@/types/snapshot';

/** Loads the authoritative opportunity detail directly from PostgreSQL via API. */
export async function fetchSnapshot(slug: string): Promise<OpportunitySnapshot | null> {
  try {
    return await jobsApiGet<OpportunitySnapshot>(`/api/jobs/${encodeURIComponent(slug)}`);
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return null;
    throw error;
  }
}
