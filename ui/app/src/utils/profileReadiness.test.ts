import { describe, expect, it } from 'vitest';
import { evaluateProfileReadiness, mergeCVIntoFields } from './profileReadiness';

const longCV =
  'Jane Doe\nExperience\nSoftware Engineer with 8 years building APIs and cloud systems in Kenya. Education BSc CS. Skills Go, TypeScript, PostgreSQL.';

describe('evaluateProfileReadiness', () => {
  it('is ready when placement_ready', () => {
    const r = evaluateProfileReadiness({
      ok: true,
      present: true,
      placement_ready: true,
      extracted_text: 'short',
    });
    expect(r.ready).toBe(true);
    expect(r.placementReady).toBe(true);
  });

  it('is not ready without CV or chat fields', () => {
    const r = evaluateProfileReadiness({ ok: true, present: false }, {});
    expect(r.ready).toBe(false);
    expect(r.cvPresent).toBe(false);
  });

  it('is ready when CV present and chat fields complete', () => {
    const r = evaluateProfileReadiness(
      {
        ok: true,
        present: true,
        placement_ready: false,
        extracted_text: longCV,
      },
      {
        target_job_title: 'Backend Engineer',
        job_types: ['Full-time'],
        preferred_countries: ['KE'],
        experience_level: 'senior',
        salary_min: 100000,
        salary_max: 200000,
        currency: 'KES',
      }
    );
    expect(r.cvPresent).toBe(true);
    expect(r.chatReady).toBe(true);
    expect(r.ready).toBe(true);
    expect(r.matchCapable).toBe(true);
    expect(r.preferenceMissing).toEqual([]);
  });

  it('is matchCapable with CV even when preferences incomplete', () => {
    // After onboarding upload: CV on file, salary/countries may still lag.
    // Matches must not treat this as “finish your CV”.
    const r = evaluateProfileReadiness(
      {
        ok: true,
        present: true,
        placement_ready: false,
        extracted_text: longCV,
      },
      {
        target_job_title: 'Backend Engineer',
        job_types: ['Full-time'],
        experience_level: 'senior',
        // no salary, no preferred_countries
      }
    );
    expect(r.cvPresent).toBe(true);
    expect(r.ready).toBe(false);
    expect(r.matchCapable).toBe(true);
    expect(r.preferenceMissing).toEqual(
      expect.arrayContaining(['salary_expectation', 'preferred_countries'])
    );
    expect(r.preferenceMissing).not.toContain('capabilities');
    expect(r.missing).not.toContain('capabilities');
  });

  it('is not matchCapable without CV or capabilities', () => {
    const r = evaluateProfileReadiness({ ok: true, present: false }, {
      target_job_title: 'PM',
    } as never);
    expect(r.matchCapable).toBe(false);
    expect(r.cvPresent).toBe(false);
  });

  it('merges CV text into fields', () => {
    const f = mergeCVIntoFields(
      {},
      { ok: true, present: true, extracted_text: 'Long resume body here' }
    );
    expect(f.extra_info).toContain('Long resume');
  });
});
