// Mirrors pkg/events/v1/canonicals.go (CanonicalUpsertedV1) — the JSON
// shape published to R2 at /<prefix>/<slug>.json. Polymorphic across
// the five opportunity kinds; per-kind fields live under `attributes`.
//
// When schema_version bumps, widen the type and branch on version at
// the call site.

export type OpportunityKind = "job" | "scholarship" | "tender" | "deal" | "funding";

export interface AnchorLocation {
  country?: string;
  region?: string;
  city?: string;
  lat?: number;
  lon?: number;
}

export interface OpportunitySnapshot {
  schema_version: 1;
  kind: OpportunityKind;
  id: string;
  slug: string;
  title: string;
  description?: string;          // markdown body
  issuing_entity: string;         // was company.name on JobSnapshot
  apply_url: string;
  posted_at: string;              // RFC3339
  deadline?: string;              // RFC3339, optional
  anchor_location?: AnchorLocation;
  remote?: boolean;
  geo_scope?: "global" | "regional" | "national" | "local";

  // Universal monetary (semantics determined by kind: salary/stipend/budget/discount/grant)
  amount_min?: number;
  amount_max?: number;
  currency?: string;

  // Universal taxonomy
  categories?: string[];

  // Kind-specific extension
  attributes?: Record<string, unknown>;

  // Optional pre-rendered HTML (legacy jobs path); preferred is `description` markdown.
  description_html?: string;
  // Optional translation/locale metadata (carried through from older job snapshots).
  language?: string;
  expires_at?: string;
  is_featured?: boolean;
  quality_score?: number;
}

// Type guards for kind-specific narrowing.
export function isJob(o: OpportunitySnapshot): boolean { return o.kind === "job"; }
export function isScholarship(o: OpportunitySnapshot): boolean { return o.kind === "scholarship"; }
export function isTender(o: OpportunitySnapshot): boolean { return o.kind === "tender"; }
export function isDeal(o: OpportunitySnapshot): boolean { return o.kind === "deal"; }
export function isFunding(o: OpportunitySnapshot): boolean { return o.kind === "funding"; }

// Convenience accessors for common kind-specific attribute keys, with safe defaults.
export function jobEmploymentType(o: OpportunitySnapshot): string | undefined {
  return typeof o.attributes?.employment_type === "string" ? o.attributes.employment_type as string : undefined;
}
export function jobSeniority(o: OpportunitySnapshot): string | undefined {
  return typeof o.attributes?.seniority === "string" ? o.attributes.seniority as string : undefined;
}
export function scholarshipFieldOfStudy(o: OpportunitySnapshot): string | undefined {
  return typeof o.attributes?.field_of_study === "string" ? o.attributes.field_of_study as string : undefined;
}
export function scholarshipDegreeLevel(o: OpportunitySnapshot): string | undefined {
  return typeof o.attributes?.degree_level === "string" ? o.attributes.degree_level as string : undefined;
}
export function tenderProcurementDomain(o: OpportunitySnapshot): string | undefined {
  return typeof o.attributes?.procurement_domain === "string" ? o.attributes.procurement_domain as string : undefined;
}
export function dealDiscountPercent(o: OpportunitySnapshot): number | undefined {
  return typeof o.attributes?.discount_percent === "number" ? o.attributes.discount_percent as number : undefined;
}
export function fundingFocusArea(o: OpportunitySnapshot): string | undefined {
  return typeof o.attributes?.focus_area === "string" ? o.attributes.focus_area as string : undefined;
}

