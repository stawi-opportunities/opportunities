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

  it('uses profile-fields name and phone when extras omitted', () => {
    const doc = hydrateStructuredCV({
      name: 'Ada Lovelace',
      phone: '+44 20 0000',
      current_title: 'Analyst',
      strong_skills: ['Math'],
    });
    expect(doc.basics.name).toBe('Ada Lovelace');
    expect(doc.basics.phone).toBe('+44 20 0000');
    expect(doc.basics.phones).toEqual(['+44 20 0000']);
    expect(doc.basics.headline).toBe('Analyst');
    expect(doc.skills.strong).toEqual(['Math']);
  });

  it('hydrates multiple phones and emails', () => {
    const doc = hydrateStructuredCV(
      {
        name: 'Jane',
        phone: '+254 700 111 222 · +254 733 000 111',
        emails: ['a@work.com', 'b@home.com'],
        bio: 'Full about section with plenty of detail about impact and scope.',
      },
      { phones: ['+254 700 111 222', '+254 733 000 111'] }
    );
    expect(doc.basics.phones.length).toBeGreaterThanOrEqual(2);
    expect(doc.basics.emails).toEqual(['a@work.com', 'b@home.com']);
    expect(doc.summary).toMatch(/Full about/);
    const pf = structuredCVToProfileFields(doc);
    expect(pf.phone).toContain('·');
    expect(pf.emails).toHaveLength(2);
  });

  it('prefers extras over profile-fields for name', () => {
    const doc = hydrateStructuredCV({ name: 'From PF' }, { name: 'From Extra' });
    expect(doc.basics.name).toBe('From Extra');
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
