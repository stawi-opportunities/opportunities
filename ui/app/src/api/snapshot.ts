import { getConfig } from "@/utils/config";
import type { JobSnapshot } from "@/types/snapshot";

/** GET jobs-repo.stawi.org/jobs/<slug>.json. Returns null on 404. */
export async function fetchSnapshot(slug: string): Promise<JobSnapshot | null> {
  const url = `${getConfig().contentOrigin}/jobs/${encodeURIComponent(slug)}.json`;
  const res = await fetch(url, { credentials: "omit" });
  if (res.status === 404) return null;
  if (!res.ok) {
    throw new Error(`snapshot ${slug}: HTTP ${res.status}`);
  }
  return (await res.json()) as JobSnapshot;
}
