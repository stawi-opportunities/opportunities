/**
 * Client-side chat extraction used when POST /me/chat is unavailable
 * (older matching binary) or fails. Mirrors the Go heuristic in
 * me_onboarding_chat.go so local/dev UX stays complete before redeploy.
 */

import type { OnboardingChatFields, OnboardingChatResponse } from '@/api/candidates';

/** Required keys that gate plan selection (aligned with server). */
const REQUIRED = [
  'target_job_title',
  'capabilities',
  'job_types',
  'salary_expectation',
  'preferred_countries',
  'experience_level',
] as const;

export function missingChatFields(f: OnboardingChatFields): string[] {
  const miss: string[] = [];
  if (!f.target_job_title?.trim()) miss.push('target_job_title');
  if (!hasCapabilities(f)) miss.push('capabilities');
  if (!f.job_types?.length) miss.push('job_types');
  if (!hasSalary(f)) miss.push('salary_expectation');
  if (!f.preferred_countries?.length) miss.push('preferred_countries');
  if (!normalizeExperience(f.experience_level)) miss.push('experience_level');
  return miss;
}

export function isChatReady(f: OnboardingChatFields): boolean {
  return missingChatFields(f).length === 0;
}

function hasCapabilities(f: OnboardingChatFields): boolean {
  // CV only — LinkedIn is optional and does not unlock readiness.
  const t = (f.extra_info ?? '').trim();
  if (looksLikeCV(t)) return true;
  // File-backed resume already stored (server hydrate marker).
  const low = t.toLowerCase();
  if (
    t.length >= 40 &&
    (low.includes('cv on file') ||
      low.includes('uploaded cv') ||
      low.includes('resume document stored'))
  ) {
    return true;
  }
  // Substantial free-form work history without standard section headers.
  if (t.length >= 400) return true;
  return false;
}

function looksLikeCV(s?: string): boolean {
  const t = (s ?? '').trim();
  if (t.length < 80) return false;
  const low = t.toLowerCase();
  const markers = [
    'experience',
    'education',
    'skills',
    'curriculum',
    'resume',
    'cv',
    'worked at',
    'mba',
    'bsc',
    'msc',
    'university',
    'years of experience',
  ];
  const hits = markers.filter((m) => low.includes(m)).length;
  // Require CV markers so short preference chat cannot stack into a fake resume.
  if (hits >= 2) return true;
  if (hits >= 1 && t.length >= 200) return true;
  if (hits >= 1 && t.length >= 600) return true;
  return false;
}

function hasSalary(f: OnboardingChatFields): boolean {
  return (f.salary_min != null && f.salary_min > 0) || (f.salary_max != null && f.salary_max > 0);
}

function questionForMissing(key: string): string {
  switch (key) {
    case 'target_job_title':
      return 'What role should we match you to? (e.g. Senior Software Engineer, Product Manager)';
    case 'capabilities':
      return 'Please attach or paste your CV so we can understand your capabilities.';
    case 'job_types':
      return 'What kinds of jobs should we notify you about? (Full-time, Part-time, Contract, Freelance, Internship)';
    case 'salary_expectation':
      return 'What are your salary expectations? (e.g. USD 80k–120k, or KES 200000+)';
    case 'preferred_countries':
      return 'Which countries should we source opportunities from? (e.g. Kenya, Nigeria, remote US)';
    case 'experience_level':
      return "What's your experience level — entry, junior, mid, senior, lead, or executive?";
    default:
      return 'Could you share a bit more so we can match the right opportunities?';
  }
}

function formatSalaryAck(f: OnboardingChatFields): string {
  const cur = f.currency || 'USD';
  if (f.salary_min != null && f.salary_max != null && f.salary_min !== f.salary_max) {
    return `${cur} ${f.salary_min}–${f.salary_max}`;
  }
  if (f.salary_min != null) return `${cur} ${f.salary_min}+`;
  if (f.salary_max != null) return `${cur} up to ${f.salary_max}`;
  return '';
}

/** Summarize filled required fields so the next ask feels grounded. */
function acknowledgeKnown(f: OnboardingChatFields): string {
  const parts: string[] = [];
  if (f.target_job_title?.trim()) parts.push(f.target_job_title.trim());
  const exp = normalizeExperience(f.experience_level);
  if (exp) parts.push(`${exp} level`);
  if (f.job_types?.length) parts.push(f.job_types.join('/'));
  if (hasSalary(f)) {
    const s = formatSalaryAck(f);
    if (s) parts.push(s);
  }
  if (f.preferred_countries?.length) {
    parts.push(`opportunities from ${f.preferred_countries.join(', ')}`);
  } else if (f.country?.trim()) {
    parts.push(`based in ${f.country.trim()}`);
  }
  if (hasCapabilities(f)) parts.push('CV on file');
  if (!parts.length) return '';
  return `Got it — ${parts.join(' · ')}.`;
}

const FIELD_WHY: Record<string, string> = {
  target_job_title: 'Matching scores opportunities against a concrete role title.',
  capabilities: 'Your resume is how we understand skills and experience depth.',
  job_types: 'We only notify you about employment kinds you care about.',
  salary_expectation: 'Salary filters avoid poor-fit listings and improve ranking.',
  preferred_countries: 'We source and rank opportunities from the markets you choose.',
  experience_level: 'Level steers seniority-appropriate matches.',
};

export function followUpReply(f: OnboardingChatFields): string {
  const miss = missingChatFields(f);
  if (miss.length === 0) {
    const ack = acknowledgeKnown(f);
    return ack
      ? `${ack} Your placement profile looks complete. Choose a plan to start matching.`
      : 'Thanks — I have everything I need to match opportunities. Choose a plan to start.';
  }
  const next = questionForMissing(miss[0]!);
  const why = FIELD_WHY[miss[0]!] ? ` ${FIELD_WHY[miss[0]!]}` : '';
  const ack = acknowledgeKnown(f);
  if (!ack) {
    if (miss.length > 1) {
      return `${next}${why} After that I still need ${miss.length - 1} more detail(s).`;
    }
    return `${next}${why}`;
  }
  if (miss.length > 1) {
    return `${ack} ${next}${why} (${miss.length - 1} more after that.)`;
  }
  return `${ack} ${next}${why}`;
}

export function mergeChatFields(
  base: OnboardingChatFields,
  overlay: OnboardingChatFields
): OnboardingChatFields {
  const out: OnboardingChatFields = { ...base };
  if (overlay.target_job_title?.trim()) out.target_job_title = overlay.target_job_title.trim();
  const exp = normalizeExperience(overlay.experience_level);
  if (exp) out.experience_level = exp;
  const st = normalizeSearchStatus(overlay.job_search_status);
  if (st) out.job_search_status = st;
  if (overlay.salary_min != null) out.salary_min = overlay.salary_min;
  if (overlay.salary_max != null) out.salary_max = overlay.salary_max;
  if (overlay.currency?.trim()) out.currency = overlay.currency.trim().toUpperCase();
  if (overlay.preferred_regions?.length) out.preferred_regions = unique(overlay.preferred_regions);
  if (overlay.preferred_countries?.length)
    out.preferred_countries = unique(overlay.preferred_countries.map((c) => c.toUpperCase()));
  if (overlay.preferred_timezones?.length)
    out.preferred_timezones = unique(overlay.preferred_timezones);
  if (overlay.preferred_languages?.length)
    out.preferred_languages = unique(overlay.preferred_languages);
  if (overlay.job_types?.length) out.job_types = unique(overlay.job_types);
  if (overlay.country?.trim()) out.country = normalizeCountry(overlay.country);
  const li = normalizeLinkedIn(overlay.linkedin);
  if (li) out.linkedin = li;
  if (overlay.extra_info?.trim()) {
    const next = overlay.extra_info.trim();
    if (!out.extra_info || next.length >= out.extra_info.length) out.extra_info = next;
  }

  // Safe defaults — never invent title/linkedin/salary.
  if (!out.job_search_status && out.target_job_title) {
    out.job_search_status = 'actively_looking';
  }
  if (
    !out.preferred_languages?.length &&
    (out.target_job_title || (out.extra_info?.length ?? 0) > 80)
  ) {
    out.preferred_languages = ['English'];
  }
  if (!out.preferred_regions?.length && out.country) {
    out.preferred_regions = regionForCountry(out.country);
  }
  if (!out.preferred_countries?.length && out.country) {
    out.preferred_countries = [out.country];
  }
  if (!out.job_types?.length && out.target_job_title) {
    out.job_types = ['Full-time'];
  }
  if (!out.currency && hasSalary(out)) out.currency = 'USD';
  return out;
}

export function heuristicExtract(msg: string): OnboardingChatFields {
  const low = msg.toLowerCase();
  const f: OnboardingChatFields = {};

  if (/\b(executive|director|vp|c-level|cxo)\b/i.test(msg)) f.experience_level = 'executive';
  else if (/\b(lead|staff|principal)\b/i.test(msg)) f.experience_level = 'lead';
  else if (/\b(senior|sr\.?)\b/i.test(msg)) f.experience_level = 'senior';
  else if (/\b(mid[- ]?level|intermediate)\b/i.test(msg)) f.experience_level = 'mid';
  else if (/\b(junior|jr\.?)\b/i.test(msg)) f.experience_level = 'junior';
  else if (/\b(entry[- ]?level|intern|graduate|new grad)\b/i.test(msg))
    f.experience_level = 'entry';
  else if (/\b(\d+)\+?\s*years?\b/i.test(msg)) {
    const m = msg.match(/\b(\d+)\+?\s*years?\b/i);
    const n = m ? parseInt(m[1]!, 10) : 0;
    if (n >= 10) f.experience_level = 'lead';
    else if (n >= 5) f.experience_level = 'senior';
    else if (n >= 2) f.experience_level = 'mid';
    else f.experience_level = 'junior';
  }

  if (/\bactively\s+(looking|seeking)\b/i.test(msg)) f.job_search_status = 'actively_looking';
  else if (/\bopen\s+to\s+(offers?|opportunities)\b/i.test(msg))
    f.job_search_status = 'open_to_offers';
  else if (/\b(casually|just browsing)\b/i.test(msg)) f.job_search_status = 'casually_browsing';

  const types: string[] = [];
  if (/\bfull[-\s]?time\b/i.test(msg)) types.push('Full-time');
  if (/\bpart[-\s]?time\b/i.test(msg)) types.push('Part-time');
  if (/\bcontract(or|ing)?\b/i.test(msg)) types.push('Contract');
  if (/\bfreelance\b/i.test(msg)) types.push('Freelance');
  if (/\bintern(ship)?\b/i.test(msg)) types.push('Internship');
  if (/\bremote\b/i.test(msg) && types.length === 0) types.push('Full-time');
  f.job_types = unique(types);

  const regions: string[] = [];
  for (const r of [
    'Anywhere',
    'Africa',
    'Europe',
    'North America',
    'South America',
    'Asia',
    'Oceania',
    'Middle East',
  ]) {
    if (low.includes(r.toLowerCase())) regions.push(r);
  }
  if (/\b(worldwide|global|anywhere)\b/i.test(msg)) regions.push('Anywhere');
  f.preferred_regions = unique(regions);

  const langs: string[] = [];
  for (const l of [
    'English',
    'French',
    'Arabic',
    'Swahili',
    'Portuguese',
    'Spanish',
    'German',
    'Mandarin',
  ]) {
    if (low.includes(l.toLowerCase())) langs.push(l);
  }
  f.preferred_languages = unique(langs);

  const countryHints: [RegExp, string][] = [
    [/\b(kenya|nairobi)\b/i, 'KE'],
    [/\b(uganda|kampala)\b/i, 'UG'],
    [/\b(nigeria|lagos|abuja)\b/i, 'NG'],
    [/\b(ghana|accra)\b/i, 'GH'],
    [/\b(south africa|johannesburg|cape town)\b/i, 'ZA'],
    [/\b(united states|u\.s\.a\.|usa|\bu\.s\.\b)\b/i, 'US'],
    [/\b(united kingdom|\buk\b|london)\b/i, 'GB'],
    [/\b(germany|berlin)\b/i, 'DE'],
    [/\b(france|paris)\b/i, 'FR'],
    [/\b(india|bangalore|mumbai)\b/i, 'IN'],
    [/\b(philippines|manila)\b/i, 'PH'],
    [/\b(rwanda|kigali)\b/i, 'RW'],
    [/\b(tanzania|dar es salaam)\b/i, 'TZ'],
    [/\b(ethiopia|addis)\b/i, 'ET'],
    [/\b(egypt|cairo)\b/i, 'EG'],
    [/\b(morocco|casablanca)\b/i, 'MA'],
  ];
  const found: string[] = [];
  for (const [re, code] of countryHints) {
    if (re.test(msg) && !found.includes(code)) found.push(code);
  }
  if (found.length) {
    f.country = found[0];
    f.preferred_countries = found;
  }

  const li = extractLinkedIn(msg);
  if (li) f.linkedin = li;

  // Salary: "$80k", "KES 200000", "80000 USD"
  const sal = msg.match(
    /(?:(USD|EUR|GBP|KES|NGN|ZAR|GHS|AED|INR)\s*)?\$?\s*([\d,]+)\s*([kK])?(?:\s*[-–to]+\s*\$?\s*([\d,]+)\s*([kK])?)?(?:\s*(USD|EUR|GBP|KES|NGN|ZAR|GHS|AED|INR))?/
  );
  if (sal) {
    const cur = (sal[1] || sal[6] || 'USD').toUpperCase();
    const n1 = parseMoney(sal[2], sal[3]);
    const n2 = sal[4] ? parseMoney(sal[4], sal[5]) : n1;
    if (n1 >= 1000) {
      f.currency = cur;
      f.salary_min = Math.min(n1, n2);
      f.salary_max = Math.max(n1, n2);
    }
  }

  f.target_job_title = guessTitle(msg);
  // Only store as capabilities when the text looks like a CV / work history.
  if (looksLikeCV(msg)) {
    f.extra_info = msg.slice(0, 8000);
  }
  return f;
}

function extractLinkedIn(msg: string): string | undefined {
  const url = msg.match(/linkedin\.com\/in\/[A-Za-z0-9\-_%]+/i);
  if (url) return normalizeLinkedIn(url[0]);
  const labeled = msg.match(
    /(?:linkedin\s*(?:is|:)?\s*|my\s+linkedin\s*(?:is|:)?\s*)(@?[A-Za-z0-9\-_]{2,80})/i
  );
  if (labeled?.[1]) return normalizeLinkedIn(labeled[1]);
  return undefined;
}

function normalizeLinkedIn(s?: string): string | undefined {
  if (!s?.trim()) return undefined;
  let v = s
    .trim()
    .replace(/^https?:\/\//i, '')
    .replace(/^www\./i, '')
    .replace(/\/+$/, '');
  if (/linkedin\.com\/in\//i.test(v)) return `https://${v.toLowerCase()}`;
  v = v.replace(/^@/, '');
  if (v.length < 2 || v.length > 100 || /\s/.test(v)) return undefined;
  return v;
}

/** Concatenate user turns so multi-turn answers re-extract cleanly. */
function collectUserCorpus(
  history: { role: 'user' | 'assistant'; content: string }[],
  latest: string
): string {
  const parts: string[] = [];
  for (const m of history) {
    if (m.role === 'user' && m.content?.trim()) parts.push(m.content.trim());
  }
  if (latest.trim()) parts.push(latest.trim());
  return parts.join('\n\n');
}

/** Run a local chat turn (no network). Preserves prior history for continuity. */
export function localChatTurn(
  message: string,
  draft: OnboardingChatFields,
  history: { role: 'user' | 'assistant'; content: string }[] = []
): OnboardingChatResponse {
  const prior = history.filter((m) => m.role === 'user' || m.role === 'assistant');
  const corpus = collectUserCorpus(prior, message);
  // Re-read the full seeker corpus so earlier answers aren't lost when the
  // latest turn only fills one gap.
  const extracted = heuristicExtract(corpus);
  const base: OnboardingChatFields = { ...draft };
  // Keep an existing CV; only replace/add when this message (or corpus) is CV-like.
  if (looksLikeCV(message)) {
    base.extra_info = message.trim().slice(0, 8000);
  } else if (looksLikeCV(draft.extra_info)) {
    base.extra_info = draft.extra_info;
  } else if (looksLikeCV(extracted.extra_info)) {
    base.extra_info = extracted.extra_info;
  } else {
    // Do not accumulate short chat turns into fake capabilities.
    delete base.extra_info;
  }
  const fields = mergeChatFields(base, extracted);
  // Restore CV if merge dropped a longer prior CV for a non-CV extract.
  if (looksLikeCV(draft.extra_info) && !looksLikeCV(fields.extra_info)) {
    fields.extra_info = draft.extra_info;
  }
  const missing = missingChatFields(fields);
  const ready = missing.length === 0;
  const reply = followUpReply(fields);
  return {
    reply,
    fields,
    missing,
    ready,
    messages: [
      ...prior,
      { role: 'user', content: message.trim() },
      { role: 'assistant', content: reply },
    ],
    source: 'heuristic',
    field_status: Object.fromEntries(
      REQUIRED.map((k) => {
        const ok = !missing.includes(k);
        return [
          k,
          {
            ok,
            reason: ok ? undefined : 'incomplete',
            value: ok ? valueHint(k, fields) : undefined,
          },
        ];
      })
    ),
  };
}

function valueHint(key: string, f: OnboardingChatFields): string | undefined {
  switch (key) {
    case 'target_job_title':
      return f.target_job_title;
    case 'capabilities':
      return looksLikeCV(f.extra_info) ? 'CV / work history provided' : undefined;
    case 'job_types':
      return f.job_types?.join(', ');
    case 'salary_expectation':
      return formatSalaryAck(f) || undefined;
    case 'preferred_countries':
      return f.preferred_countries?.join(', ') || f.country;
    case 'experience_level':
      return f.experience_level;
    default:
      return undefined;
  }
}

function parseMoney(num: string | undefined, k: string | undefined): number {
  if (!num) return 0;
  let n = parseFloat(num.replace(/,/g, ''));
  if (Number.isNaN(n)) return 0;
  if (k && k.toLowerCase() === 'k') n *= 1000;
  return n;
}

function jobishTitle(s: string): boolean {
  return /\b(engineer|developer|designer|manager|analyst|director|officer|specialist|consultant|architect|scientist|nurse|teacher|accountant|marketer|product|sales|ops)\b/i.test(
    s
  );
}

function junkTitle(s: string): boolean {
  const low = s.toLowerCase().trim();
  if (!low) return true;
  const junk = [
    'full-time',
    'full time',
    'part-time',
    'part time',
    'remote',
    'hybrid',
    'contract',
    'roles',
    'role',
    'jobs',
    'job',
    'opportunities',
    'opportunity',
    'work',
    'position',
    'looking',
    'seeking',
  ];
  return junk.some((p) => low === p || low.startsWith(p + ' '));
}

function guessTitle(msg: string): string | undefined {
  const low = msg.toLowerCase();
  const markers = [
    'i am a ',
    "i'm a ",
    'i am an ',
    "i'm an ",
    'i work as a ',
    'i work as an ',
    'work as a ',
    'work as an ',
    'seeking a ',
    'seeking an ',
    'looking for a ',
    'looking for an ',
    'want a ',
    'want an ',
    'title: ',
    'position: ',
    'target role is ',
    'my role is ',
  ];
  for (const m of markers) {
    const i = low.indexOf(m);
    if (i < 0) continue;
    let rest = msg.slice(i + m.length).trim();
    for (const sep of [
      '\n',
      '.',
      ',',
      ';',
      ' in ',
      ' based',
      ' with ',
      ' who ',
      ' looking',
      ' for full',
      ' for part',
      ' roles',
      ' role',
    ]) {
      const j = rest.toLowerCase().indexOf(sep);
      if (j > 0) rest = rest.slice(0, j);
    }
    rest = rest.replace(/\s+/g, ' ').trim();
    if (rest.length < 3 || rest.length > 80 || junkTitle(rest)) continue;
    if (jobishTitle(rest)) return rest;
    if (m === 'title: ' || m === 'position: ' || m === 'target role is ' || m === 'my role is ') {
      return rest;
    }
  }
  // "{Title} looking for …"
  if (jobishTitle(msg)) {
    for (const sep of [' looking for', ' seeking', ' interested in', ' open to']) {
      const i = low.indexOf(sep);
      if (i <= 3) continue;
      let cand = msg.slice(0, i).replace(/\s+/g, ' ').trim();
      const lastBreak = Math.max(cand.lastIndexOf('.'), cand.lastIndexOf('\n'));
      if (lastBreak >= 0) cand = cand.slice(lastBreak + 1).trim();
      if (cand.length >= 3 && cand.length <= 80 && jobishTitle(cand) && !junkTitle(cand)) {
        return cand;
      }
    }
  }
  // CV first line jobish
  for (const line of msg.split('\n')) {
    const t = line.trim();
    if (t.length < 4 || t.length > 80) continue;
    if (/@|http/i.test(t)) continue;
    if (jobishTitle(t) && !junkTitle(t)) return t;
  }
  return undefined;
}

function normalizeExperience(v?: string): string | undefined {
  if (!v) return undefined;
  const s = v.toLowerCase().trim();
  const map: Record<string, string> = {
    entry: 'entry',
    junior: 'junior',
    mid: 'mid',
    'mid-level': 'mid',
    senior: 'senior',
    lead: 'lead',
    executive: 'executive',
  };
  return map[s];
}

function normalizeSearchStatus(v?: string): string | undefined {
  if (!v) return undefined;
  const s = v.toLowerCase().trim().replace(/\s+/g, '_');
  if (['actively_looking', 'open_to_offers', 'casually_browsing'].includes(s)) return s;
  return undefined;
}

function normalizeCountry(v?: string): string {
  if (!v) return '';
  const s = v.trim().toUpperCase();
  if (s.length === 2) return s;
  const names: Record<string, string> = {
    KENYA: 'KE',
    UGANDA: 'UG',
    NIGERIA: 'NG',
    GHANA: 'GH',
    'SOUTH AFRICA': 'ZA',
    'UNITED STATES': 'US',
    USA: 'US',
    'UNITED KINGDOM': 'GB',
    UK: 'GB',
  };
  return names[s] ?? s;
}

function regionForCountry(code: string): string[] {
  const africa = new Set(['KE', 'UG', 'NG', 'GH', 'ZA', 'RW', 'TZ', 'ET', 'EG', 'MA']);
  if (africa.has(code.toUpperCase())) return ['Africa'];
  if (['US', 'CA', 'MX'].includes(code.toUpperCase())) return ['North America'];
  if (['GB', 'DE', 'FR'].includes(code.toUpperCase())) return ['Europe'];
  return ['Anywhere'];
}

function unique(arr: string[]): string[] {
  return [...new Set(arr.map((s) => s.trim()).filter(Boolean))];
}
