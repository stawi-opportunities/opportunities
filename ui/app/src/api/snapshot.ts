import { getConfig } from "@/utils/config";
import type { OpportunityKind, OpportunitySnapshot } from "@/types/snapshot";

/**
 * GET opportunities-data.stawi.org/<prefix>/<slug>.json. Returns null on 404.
 *
 * `prefix` is the kind's R2 URL segment (jobs, scholarships, tenders, deals,
 * funding). Defaults to "jobs" for backwards compatibility with the
 * job-only callsite that predates the polymorphic split.
 *
 * When `lang` is provided (and not "en" or empty), tries the translated
 * variant at `<slug>.<lang>.json` first and falls back to the base
 * snapshot on 404. The fallback path keeps the feature degrading
 * cleanly for opportunities the translator hasn't reached yet.
 *
 * Old job-shaped JSON (no `kind` field) is normalised to
 * `{ kind: "job", ... }` so the renderer can branch unconditionally.
 */
export async function fetchSnapshot(
  slug: string,
  lang?: string,
  prefix: string = "jobs",
): Promise<OpportunitySnapshot | null> {
  const origin = getConfig().contentOrigin;
  const base = `${origin}/${prefix}/${encodeURIComponent(slug)}`;

  if (lang && lang !== "en") {
    const res = await fetch(`${base}.${lang}.json`, { credentials: "omit" });
    if (res.ok) return coerce(await res.json());
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
  return coerce(await res.json());
}

/**
 * Normalises raw JSON from R2 into OpportunitySnapshot. Accepts both the
 * new polymorphic shape and the legacy job-only shape (which lacks
 * `kind`, carries `company.name` instead of `issuing_entity`, etc.).
 */
function coerce(raw: unknown): OpportunitySnapshot {
  const o = (raw ?? {}) as Record<string, unknown>;

  // New shape: has `kind` at the top level.
  if (typeof o.kind === "string") {
    return o as unknown as OpportunitySnapshot;
  }

  // Legacy shape: derive issuing_entity from company.name, default kind to "job".
  const company = (o.company ?? {}) as Record<string, unknown>;
  const issuingEntity = typeof company.name === "string" ? (company.name as string) : "";

  return {
    ...(o as object),
    kind: "job" as OpportunityKind,
    issuing_entity: issuingEntity,
  } as OpportunitySnapshot;
}
