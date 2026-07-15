import { describe, expect, it } from 'vitest';
import {
  heuristicExtract,
  isChatReady,
  localChatTurn,
  mergeChatFields,
  missingChatFields,
} from './chatHeuristic';

const miniCV = `CURRICULUM VITAE
Jane Doe — Senior Software Engineer
EXPERIENCE
2019-2024 Senior Engineer at Acme. Built APIs in Go.
EDUCATION
BSc Computer Science
SKILLS
Go, Kubernetes`;

describe('chatHeuristic', () => {
  it('extracts a complete free-form message with CV to ready=true', () => {
    const msg =
      `${miniCV}\n` +
      'Actively looking for full-time remote roles. Salary USD 90k-130k. ' +
      'Opportunities from Kenya and Nigeria. English speaker.';
    const res = localChatTurn(msg, {});
    expect(res.fields.experience_level).toBe('senior');
    expect(res.fields.country).toBe('KE');
    expect(res.fields.preferred_countries).toEqual(expect.arrayContaining(['KE', 'NG']));
    expect(res.fields.job_types?.length).toBeGreaterThan(0);
    expect(res.fields.salary_min).toBeGreaterThan(0);
    expect(res.ready).toBe(true);
    expect(res.missing).toEqual([]);
  });

  it('does not treat LinkedIn alone as capabilities', () => {
    const res = localChatTurn(
      'I am a senior software engineer in Kenya looking for full-time roles. LinkedIn: linkedin.com/in/jane',
      {}
    );
    expect(res.ready).toBe(false);
    expect(res.missing).toEqual(expect.arrayContaining(['capabilities', 'salary_expectation']));
  });

  it('asks a follow-up when only a greeting is provided', () => {
    const res = localChatTurn('hello', {});
    expect(res.ready).toBe(false);
    expect(res.missing.length).toBeGreaterThan(0);
    expect(res.reply.toLowerCase()).toMatch(/role|cv|looking|work|opportunities/);
  });

  it('processes a rich free-text turn, extracts known fields, and asks for the CV', () => {
    const res = localChatTurn(
      "I'm a senior software engineer looking for full-time remote roles. Salary USD 90k–120k. Opportunities from Kenya and Nigeria.",
      {}
    );
    expect(res.ready).toBe(false);
    expect(res.fields.target_job_title?.toLowerCase()).toMatch(/software engineer/);
    expect(res.fields.experience_level).toBe('senior');
    expect(res.fields.salary_min).toBeGreaterThan(0);
    expect(res.fields.preferred_countries).toEqual(expect.arrayContaining(['KE', 'NG']));
    expect(res.missing).toContain('capabilities');
    expect(res.reply.toLowerCase()).toMatch(/cv|resume/);
    expect(res.reply.toLowerCase()).toMatch(/got it|software engineer|90|kenya|ke/i);
  });

  it('asks only for the next gap after multi-turn answers and preserves prior fields', () => {
    let r = localChatTurn('Looking for a product manager role', {});
    expect(r.missing[0]).toBe('capabilities');
    expect(r.reply.toLowerCase()).toMatch(/cv|resume/);

    r = localChatTurn(
      `Here is my background.\n\n${miniCV}\nFull-time preferred.`,
      r.fields,
      r.messages ?? []
    );
    expect(r.fields.target_job_title?.toLowerCase()).toMatch(/product manager/);
    expect(r.missing).not.toContain('capabilities');
    // Still need salary / countries / maybe experience if not inferred
    expect(r.ready).toBe(false);
    expect(r.reply.length).toBeGreaterThan(10);
    expect(r.missing.length).toBeGreaterThan(0);
  });

  it('treats stored CV-on-file markers as capabilities without re-upload', () => {
    const f = {
      target_job_title: 'Engineer',
      experience_level: 'senior',
      job_types: ['Full-time'],
      salary_min: 90000,
      preferred_countries: ['KE'],
      extra_info:
        'Uploaded CV on file. Resume document stored for matching (experience, education, skills).',
    };
    expect(missingChatFields(f)).not.toContain('capabilities');
    expect(isChatReady(f)).toBe(true);
  });

  it('treats substantial free-form resume text as capabilities', () => {
    const long = 'A'.repeat(400) + ' professional background summary for matching.';
    expect(missingChatFields({ extra_info: long })).not.toContain('capabilities');
  });

  it('merges multi-turn answers until ready', () => {
    let draft = {};
    let r = localChatTurn('Looking for a product manager role', draft);
    draft = r.fields;
    expect(missingChatFields(draft)).toContain('capabilities');
    expect(missingChatFields(draft)).toContain('salary_expectation');

    const midCV = `CURRICULUM VITAE
Product Manager
EXPERIENCE
3 years PM at Acme.
EDUCATION
BSc Business
SKILLS
Roadmaps, research`;
    r = localChatTurn(
      `I am mid-level. Full-time. Salary expectations USD 70000. Source opportunities from Nigeria. Actively looking.\n\n${midCV}`,
      draft
    );
    expect(r.fields.country).toBe('NG');
    expect(r.fields.preferred_countries).toContain('NG');
    expect(r.fields.experience_level).toBe('mid');
    expect(r.fields.salary_min).toBeGreaterThan(0);
    expect(isChatReady(r.fields)).toBe(true);
  });

  it('pulls a title from CV-like text and treats CV as capabilities', () => {
    const cv = `Jane Doe
Senior Backend Engineer
Nairobi, Kenya

EXPERIENCE
Senior Engineer at Acme — built APIs in Go for 7 years.
EDUCATION
BSc Computer Science
SKILLS
Go, Kubernetes
Full-time, salary USD 100000, opportunities in Kenya.`;
    const f = heuristicExtract(cv);
    expect(f.target_job_title?.toLowerCase()).toMatch(/backend engineer|senior backend/);
    expect(f.country).toBe('KE');
    expect(f.experience_level).toBe('senior');
    expect(missingChatFields(mergeChatFields(f, { job_types: ['Full-time'] }))).not.toContain(
      'capabilities'
    );
  });

  it('merge prefers newer non-empty values', () => {
    const m = mergeChatFields(
      { target_job_title: 'A', country: 'KE' },
      { target_job_title: 'B', experience_level: 'senior' }
    );
    expect(m.target_job_title).toBe('B');
    expect(m.country).toBe('KE');
    expect(m.experience_level).toBe('senior');
  });
});
