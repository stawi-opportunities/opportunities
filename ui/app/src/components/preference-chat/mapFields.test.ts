import { describe, expect, it } from 'vitest';
import {
  chatFieldsToJobPreferences,
  draftToChatFields,
  fieldsToDraft,
  profileToChatFields,
  summaryChips,
} from './mapFields';

describe('preference-chat mapFields', () => {
  it('round-trips draft ↔ chat fields', () => {
    const draft = fieldsToDraft(
      {
        target_job_title: 'Engineer',
        experience_level: 'senior',
        job_search_status: 'actively_looking',
        country: 'KE',
        preferred_regions: ['Africa'],
        preferred_languages: ['English'],
        job_types: ['Full-time'],
        salary_min: 50_000,
        currency: 'USD',
      },
      'pro'
    );
    expect(draft.plan).toBe('pro');
    expect(draft.salary_range).toContain('50000');
    const back = draftToChatFields(draft);
    expect(back.target_job_title).toBe('Engineer');
    expect(back.country).toBe('KE');
  });

  it('seeds chat fields from profile', () => {
    const f = profileToChatFields({
      profile_id: 'p1',
      status: 'active',
      current_title: 'Product Manager',
      preferred_countries: 'KE, UG',
      preferred_regions: 'Africa',
      remote_preference: 'remote',
      languages: 'English;Swahili',
      plan_id: 'starter',
      subscription: 'active',
    });
    expect(f.target_job_title).toBe('Product Manager');
    expect(f.country).toBe('KE');
    expect(f.preferred_regions).toEqual(['Africa']);
    expect(f.preferred_languages).toEqual(['English', 'Swahili']);
  });

  it('maps chat fields to job preferences envelope', () => {
    const job = chatFieldsToJobPreferences({
      target_job_title: 'Data Analyst',
      experience_level: 'mid',
      job_types: ['Full-time', 'Contract'],
      country: 'ng',
      preferred_countries: ['KE', 'NG'],
      preferred_regions: ['Africa'],
      salary_min: 1000,
      currency: 'USD',
    });
    expect(job.target_roles).toEqual(['Data Analyst']);
    expect(job.seniority_levels).toEqual(['mid']);
    expect(job.employment_types).toEqual(['full-time', 'contract']);
    expect(job.locations.countries).toEqual(['KE', 'NG']);
    expect(job.locations.regions).toEqual(['Africa']);
  });

  it('builds summary chips', () => {
    const chips = summaryChips({
      target_job_title: 'Nurse',
      country: 'KE',
    });
    expect(chips.map((c) => c.key)).toEqual(['title', 'co']);
  });
});
