import type { OnboardingChatFields, OnboardingDraftFields } from '@/api/candidates';
import type { CandidateSummary } from '@/api/profile';
import type { JobPreferences } from '@/onboarding/job/v1/Flow';
import { emptyLocationPreference } from '@/onboarding/shared/LocationPicker';
import type { PlanId } from '@/utils/plans';

export const FIELD_LABELS: Record<string, string> = {
  target_job_title: 'Role',
  experience_level: 'Level',
  job_search_status: 'Search status',
  preferred_regions: 'Regions',
  preferred_countries: 'Opportunity countries',
  country: 'Home country',
  preferred_languages: 'Languages',
  job_types: 'Job types',
  salary_expectation: 'Salary',
  salary_min: 'Salary min',
  salary_max: 'Salary max',
  currency: 'Currency',
  capabilities: 'CV',
  linkedin: 'LinkedIn',
};

export function fieldsToDraft(f: OnboardingChatFields, plan?: PlanId): OnboardingDraftFields {
  const salary =
    f.salary_min != null || f.salary_max != null
      ? `${f.currency ?? 'USD'} ${f.salary_min ?? f.salary_max ?? ''}`
      : undefined;
  return {
    target_job_title: f.target_job_title,
    experience_level: f.experience_level as OnboardingDraftFields['experience_level'],
    job_search_status: f.job_search_status as OnboardingDraftFields['job_search_status'],
    salary_range: salary,
    salary_min: f.salary_min ?? undefined,
    salary_max: f.salary_max ?? undefined,
    currency: f.currency,
    wants_ats_report: true,
    preferred_regions: f.preferred_regions,
    preferred_countries: f.preferred_countries,
    preferred_timezones: f.preferred_timezones,
    preferred_languages: f.preferred_languages,
    job_types: f.job_types,
    country: f.country,
    linkedin: f.linkedin,
    extra_info: f.extra_info,
    plan,
  };
}

export function draftToChatFields(d: OnboardingDraftFields): OnboardingChatFields {
  return {
    target_job_title: d.target_job_title,
    experience_level: d.experience_level,
    job_search_status: d.job_search_status,
    preferred_regions: d.preferred_regions,
    preferred_countries: d.preferred_countries,
    preferred_timezones: d.preferred_timezones,
    preferred_languages: d.preferred_languages,
    job_types: d.job_types,
    country: d.country,
    salary_min: d.salary_min,
    salary_max: d.salary_max,
    currency: d.currency,
    linkedin: d.linkedin,
    extra_info: d.extra_info,
  };
}

/** Seed chat draft from the live candidate profile (post-onboarding refine). */
export function profileToChatFields(
  profile: CandidateSummary | null | undefined,
  draft?: OnboardingDraftFields
): OnboardingChatFields {
  const fromDraft = draft ? draftToChatFields(draft) : {};
  if (!profile) return fromDraft;

  const countries = splitList(profile.preferred_countries);
  const regions = splitList(profile.preferred_regions);
  const languages = splitList(profile.languages, /[;,]/);

  return {
    ...fromDraft,
    target_job_title: fromDraft.target_job_title || profile.current_title || undefined,
    country: fromDraft.country || countries[0] || undefined,
    preferred_countries: fromDraft.preferred_countries?.length
      ? fromDraft.preferred_countries
      : countries.length
        ? countries
        : undefined,
    preferred_regions: fromDraft.preferred_regions?.length
      ? fromDraft.preferred_regions
      : regions.length
        ? regions
        : undefined,
    preferred_languages: fromDraft.preferred_languages?.length
      ? fromDraft.preferred_languages
      : languages.length
        ? languages
        : undefined,
  };
}

/** Map chat fields into the polymorphic job preferences envelope. */
export function chatFieldsToJobPreferences(f: OnboardingChatFields): JobPreferences {
  const employment = (f.job_types ?? []).map(normalizeEmploymentType).filter(Boolean);
  const countries = f.preferred_countries?.length
    ? f.preferred_countries.map((c) => c.toUpperCase())
    : f.country?.trim()
      ? [f.country.trim().toUpperCase()]
      : [];
  return {
    target_roles: f.target_job_title?.trim() ? [f.target_job_title.trim()] : [],
    employment_types: employment,
    seniority_levels: f.experience_level ? [f.experience_level] : [],
    salary_min: f.salary_min ?? 0,
    salary_max: f.salary_max ?? f.salary_min ?? 0,
    currency: f.currency?.trim() || 'USD',
    locations: {
      ...emptyLocationPreference,
      countries,
      regions: f.preferred_regions ?? [],
      remote_ok: true,
    },
  };
}

export function summaryChips(
  f: OnboardingChatFields
): { key: string; label: string; value: string }[] {
  const chips: { key: string; label: string; value: string }[] = [];
  if (f.target_job_title) chips.push({ key: 'title', label: 'Role', value: f.target_job_title });
  if (f.experience_level) chips.push({ key: 'lvl', label: 'Level', value: f.experience_level });
  if (f.job_types?.length)
    chips.push({ key: 'jt', label: 'Notify', value: f.job_types.join(', ') });
  if (f.salary_min != null || f.salary_max != null) {
    const cur = f.currency ?? 'USD';
    const lo = f.salary_min ?? f.salary_max;
    const hi = f.salary_max ?? f.salary_min;
    chips.push({
      key: 'sal',
      label: 'Salary',
      value: lo === hi ? `${cur} ${lo}` : `${cur} ${lo}–${hi}`,
    });
  }
  if (f.preferred_countries?.length)
    chips.push({
      key: 'pc',
      label: 'Countries',
      value: f.preferred_countries.join(', '),
    });
  else if (f.country) chips.push({ key: 'co', label: 'Country', value: f.country });
  if (f.extra_info && f.extra_info.length >= 120)
    chips.push({ key: 'cv', label: 'CV', value: 'Provided' });
  if (f.preferred_regions?.length)
    chips.push({ key: 'reg', label: 'Regions', value: f.preferred_regions.join(', ') });
  return chips;
}

function splitList(raw: string | undefined | null, sep: RegExp = /[,;]/): string[] {
  if (!raw) return [];
  return raw
    .split(sep)
    .map((s) => s.trim())
    .filter(Boolean);
}

function normalizeEmploymentType(t: string): string {
  return t
    .trim()
    .toLowerCase()
    .replace(/\s+/g, '-')
    .replace('fulltime', 'full-time')
    .replace('parttime', 'part-time');
}
