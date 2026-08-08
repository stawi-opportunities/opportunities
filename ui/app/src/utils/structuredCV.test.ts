import { describe, expect, it } from 'vitest';
import {
  applyRewriteToDocument,
  hydrateStructuredCV,
  structuredCVToPlainText,
  structuredCVToProfileFields,
} from './structuredCV';

describe('structuredCV', () => {
  it('hydrates work history and skills', () => {
    const doc = hydrateStructuredCV({
      current_title: 'Engineer',
      bio: 'Builder',
      strong_skills: ['Go'],
      working_skills: ['SQL'],
      work_history: [{ title: 'Dev', company: 'Acme', start_date: '2020', summary: 'Shipped' }],
      education: 'MIT BS CS',
    });
    expect(doc.basics.headline).toBe('Engineer');
    expect(doc.experience).toHaveLength(1);
    expect(doc.experience[0]!.company).toBe('Acme');
    expect(doc.skills.strong).toContain('Go');
    expect(doc.education[0]!.school).toContain('MIT');
  });

  it('round-trips to profile fields', () => {
    const doc = hydrateStructuredCV({
      bio: 'Hi',
      strong_skills: ['Rust'],
      work_history: [{ title: 'SWE', company: 'Co', start_date: '2021', summary: 'Did things' }],
    });
    doc.basics.name = 'Ada';
    const pf = structuredCVToProfileFields(doc);
    expect(pf.name).toBe('Ada');
    expect(pf.bio).toBe('Hi');
    expect(pf.work_history?.[0]).toMatchObject({ title: 'SWE', company: 'Co' });
  });

  it('applies rewrites into experience', () => {
    let doc = hydrateStructuredCV({
      work_history: [{ title: 'A', company: 'B', summary: 'Built foo bar' }],
    });
    doc = applyRewriteToDocument(doc, 'Built foo bar', 'Delivered foo bar at scale');
    expect(doc.experience[0]!.description).toContain('at scale');
  });

  it('renders plain text for ATS', () => {
    const doc = hydrateStructuredCV({
      current_title: 'PM',
      bio: 'Leads products',
      strong_skills: ['Roadmaps'],
    });
    const text = structuredCVToPlainText(doc);
    expect(text).toMatch(/SUMMARY/);
    expect(text).toMatch(/Leads products/);
    expect(text).toMatch(/SKILLS/);
  });
});
