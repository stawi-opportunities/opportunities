import { getConfig } from "@/utils/config";
import type { JobSnapshot } from "@/types/snapshot";

/**
 * GET jobs-repo.stawi.org/jobs/<slug>.json. Returns null on 404.
 *
 * When `lang` is provided (and not "en" or empty), tries the translated
 * variant at `<slug>.<lang>.json` first and falls back to the base
 * snapshot on 404. The fallback path is what makes the feature degrade
 * cleanly for jobs the translator hasn't reached yet.
 */
export async function fetchSnapshot(
  slug: string,
  lang?: string,
): Promise<JobSnapshot | null> {
  const origin = getConfig().contentOrigin;
  const base = `${origin}/jobs/${encodeURIComponent(slug)}`;

  if (lang && lang !== "en") {
    const res = await fetch(`${base}.${lang}.json`, { credentials: "omit" });
    if (res.ok) return (await res.json()) as JobSnapshot;
    if (res.status !== 404) {
      throw new Error(`snapshot ${slug}.${lang}: HTTP ${res.status}`);
    }
    // fallthrough to the source-language snapshot
  }

  const res = await fetch(`${base}.json`, { credentials: "omit" });
  if (res.status === 404) return null;
  if (!res.ok) {
    throw new Error(`snapshot ${slug}: HTTP ${res.status}`);
  }
  return (await res.json()) as JobSnapshot;
}
