/**
 * Structured CV document — LinkedIn-style sections for edit, export, ATS.
 * Hydrated from GET /api/me/profile-fields + /me/cv; saved via PUT profile-fields.
 */

export type CVSource = 'upload' | 'manual' | 'linkedin_import' | 'merged' | 'profile';

export interface CVExperience {
  id: string;
  title: string;
  company: string;
  location?: string;
  start: string;
  end?: string;
  current?: boolean;
  description: string;
}

export interface CVEducation {
  id: string;
  school: string;
  degree?: string;
  field?: string;
  start?: string;
  end?: string;
  notes?: string;
}

export interface StructuredCV {
  basics: {
    name: string;
    headline: string;
    email?: string;
    phone?: string;
    location?: string;
  };
  summary: string;
  experience: CVExperience[];
  education: CVEducation[];
  skills: {
    strong: string[];
    working: string[];
    tools: string[];
  };
  certifications: string[];
  languages: string[];
  source: CVSource;
  updated_at?: string;
}

/** Shape returned by GET /api/me/profile-fields (and PUT body). */
export interface ProfileFieldsPayload {
  candidate_id?: string;
  name?: string;
  phone?: string;
  current_title?: string;
  target_job_title?: string;
  seniority?: string;
  experience_level?: string;
  years_experience?: number;
  skills?: string[];
  strong_skills?: string[];
  working_skills?: string[];
  tools_frameworks?: string[];
  certifications?: string[];
  preferred_roles?: string[];
  industries?: string[];
  education?: string;
  languages?: string[];
  bio?: string;
  preferred_locations?: string[];
  preferred_countries?: string[];
  preferred_regions?: string[];
  preferred_timezones?: string[];
  remote_preference?: string;
  job_search_status?: string;
  salary_min?: number;
  salary_max?: number;
  currency?: string;
  us_work_auth?: boolean | null;
  needs_sponsorship?: boolean | null;
  work_history?: Array<Record<string, unknown>>;
}

function newId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return `id_${Date.now()}_${Math.random().toString(36).slice(2, 9)}`;
}

function asString(v: unknown): string {
  if (v == null) return '';
  return String(v).trim();
}

function historyFromMaps(rows: Array<Record<string, unknown>> | undefined): CVExperience[] {
  if (!rows?.length) return [];
  return rows.map((r) => ({
    id: asString(r.id) || newId(),
    title: asString(r.title ?? r.role ?? r.job_title),
    company: asString(r.company ?? r.employer ?? r.organization),
    location: asString(r.location) || undefined,
    start: asString(r.start_date ?? r.start ?? r.from),
    end: asString(r.end_date ?? r.end ?? r.to) || undefined,
    current: Boolean(r.current ?? r.is_current),
    description: asString(r.summary ?? r.description ?? r.highlights),
  }));
}

function educationFromText(education: string | undefined): CVEducation[] {
  const t = education?.trim();
  if (!t) return [];
  // Single free-text education → one editable card (LinkedIn-like later multi-entry).
  return [
    {
      id: newId(),
      school: t,
      degree: '',
      field: '',
      notes: '',
    },
  ];
}

/** Build StructuredCV from profile-fields + optional name/phone. */
export function hydrateStructuredCV(
  pf: ProfileFieldsPayload | null | undefined,
  extras?: { name?: string; phone?: string; email?: string }
): StructuredCV {
  const strong = pf?.strong_skills?.length ? pf.strong_skills : (pf?.skills ?? []);
  const location = pf?.preferred_locations?.[0] || pf?.preferred_countries?.[0] || undefined;

  return {
    basics: {
      name: extras?.name?.trim() || pf?.name?.trim() || '',
      headline: pf?.current_title?.trim() || pf?.target_job_title?.trim() || '',
      email: extras?.email?.trim() || undefined,
      phone: extras?.phone?.trim() || pf?.phone?.trim() || '',
      location,
    },
    summary: pf?.bio?.trim() || '',
    experience: historyFromMaps(pf?.work_history),
    education: educationFromText(pf?.education),
    skills: {
      strong: [...strong],
      working: [...(pf?.working_skills ?? [])],
      tools: [...(pf?.tools_frameworks ?? [])],
    },
    certifications: [...(pf?.certifications ?? [])],
    languages: [...(pf?.languages ?? [])],
    source: 'profile',
    updated_at: new Date().toISOString(),
  };
}

/** Flatten structured CV to plain text for ATS scoring. */
export function structuredCVToPlainText(doc: StructuredCV): string {
  const lines: string[] = [];
  const { basics, summary, experience, education, skills, certifications, languages } = doc;

  if (basics.name) lines.push(basics.name);
  if (basics.headline) lines.push(basics.headline);
  const contact = [basics.location, basics.phone, basics.email].filter(Boolean).join(' · ');
  if (contact) lines.push(contact);
  lines.push('');

  if (summary.trim()) {
    lines.push('SUMMARY');
    lines.push(summary.trim());
    lines.push('');
  }

  if (experience.length) {
    lines.push('EXPERIENCE');
    for (const e of experience) {
      const dates = [e.start, e.current ? 'Present' : e.end].filter(Boolean).join(' – ');
      lines.push(`${e.title}${e.company ? ` at ${e.company}` : ''}${dates ? ` (${dates})` : ''}`);
      if (e.location) lines.push(e.location);
      if (e.description.trim()) lines.push(e.description.trim());
      lines.push('');
    }
  }

  if (education.length) {
    lines.push('EDUCATION');
    for (const ed of education) {
      const deg = [ed.degree, ed.field].filter(Boolean).join(', ');
      lines.push(`${ed.school}${deg ? ` — ${deg}` : ''}`);
      const dates = [ed.start, ed.end].filter(Boolean).join(' – ');
      if (dates) lines.push(dates);
      if (ed.notes?.trim()) lines.push(ed.notes.trim());
      lines.push('');
    }
  }

  const allSkills = [...skills.strong, ...skills.working, ...skills.tools].filter(Boolean);
  if (allSkills.length) {
    lines.push('SKILLS');
    lines.push(allSkills.join(', '));
    lines.push('');
  }

  if (certifications.length) {
    lines.push('CERTIFICATIONS');
    lines.push(certifications.join(', '));
    lines.push('');
  }

  if (languages.length) {
    lines.push('LANGUAGES');
    lines.push(languages.join(', '));
  }

  return lines.join('\n').trim();
}

/** Map StructuredCV back to profile-fields PUT body (CV sections only). */
export function structuredCVToProfileFields(doc: StructuredCV): Partial<ProfileFieldsPayload> {
  const work_history = doc.experience.map((e) => ({
    id: e.id,
    title: e.title,
    company: e.company,
    location: e.location ?? '',
    start_date: e.start,
    end_date: e.current ? '' : (e.end ?? ''),
    current: Boolean(e.current),
    summary: e.description,
  }));

  const educationText = doc.education
    .map((ed) => {
      const parts = [ed.school, ed.degree, ed.field, ed.notes].filter(Boolean);
      return parts.join(' — ');
    })
    .filter(Boolean)
    .join('\n');

  return {
    name: doc.basics.name || undefined,
    phone: doc.basics.phone || undefined,
    current_title: doc.basics.headline || undefined,
    bio: doc.summary || undefined,
    strong_skills: doc.skills.strong,
    working_skills: doc.skills.working,
    tools_frameworks: doc.skills.tools,
    skills: doc.skills.strong.length ? doc.skills.strong : doc.skills.working,
    certifications: doc.certifications,
    languages: doc.languages,
    education: educationText,
    work_history,
  };
}

export function emptyExperience(): CVExperience {
  return {
    id: newId(),
    title: '',
    company: '',
    start: '',
    end: '',
    current: false,
    description: '',
  };
}

export function emptyEducation(): CVEducation {
  return {
    id: newId(),
    school: '',
    degree: '',
    field: '',
    start: '',
    end: '',
    notes: '',
  };
}

/** Apply a rewrite string replace into summary or experience descriptions. */
export function applyRewriteToDocument(
  doc: StructuredCV,
  before: string,
  after: string
): StructuredCV {
  if (!before) {
    return {
      ...doc,
      summary: doc.summary ? `${doc.summary.trim()}\n\n${after}` : after,
      source: 'manual',
      updated_at: new Date().toISOString(),
    };
  }
  let applied = false;
  const next = { ...doc, experience: doc.experience.map((e) => ({ ...e })) };

  if (next.summary.includes(before)) {
    next.summary = next.summary.split(before).join(after);
    applied = true;
  }
  for (const e of next.experience) {
    if (e.description.includes(before)) {
      e.description = e.description.split(before).join(after);
      applied = true;
    }
  }
  if (!applied) {
    next.summary = next.summary ? `${next.summary.trim()}\n\n${after}` : after;
  }
  next.source = 'manual';
  next.updated_at = new Date().toISOString();
  return next;
}

export function applyAllRewritesToDocument(
  doc: StructuredCV,
  rewrites: { before: string; after: string }[]
): StructuredCV {
  return rewrites.reduce((d, r) => applyRewriteToDocument(d, r.before, r.after), doc);
}
