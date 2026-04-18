// Mirrors pkg/domain/snapshot.go (JobSnapshot v1).
// When schema_version bumps, widen this type and branch on version at the
// call site.

export interface JobSnapshot {
  schema_version: 1;
  id: number;
  slug: string;
  title: string;
  company: CompanyRef;
  category: string;
  location: LocationRef;
  employment: EmploymentRef;
  compensation: CompensationRef;
  skills: SkillsRef;
  description_html: string;
  apply_url?: string;
  posted_at?: string;
  updated_at?: string;
  expires_at?: string;
  quality_score: number;
  is_featured: boolean;
  /** ISO 639-1 source language. Missing on pre-translation snapshots. */
  language?: string;
}

export interface CompanyRef {
  name: string;
  slug: string;
  logo_url?: string;
  verified: boolean;
}

export interface LocationRef {
  text?: string;
  remote_type?: string;
  country?: string;
}

export interface EmploymentRef {
  type?: string;
  seniority?: string;
}

export interface CompensationRef {
  min?: number;
  max?: number;
  currency?: string;
  period?: string;
}

export interface SkillsRef {
  required: string[];
  nice_to_have: string[];
}
