/**
 * Client-side onboarding extraction used when POST /me/onboarding/chat is
 * unavailable (older matching binary) or fails. Mirrors the Go heuristic in
 * me_onboarding_chat.go so local/dev UX stays complete before redeploy.
 */

import type { OnboardingChatFields, OnboardingChatResponse } from '@/api/candidates';

const REQUIRED = [
  'target_job_title',
  'experience_level',
  'job_search_status',
  'preferred_regions',
  'country',
  'preferred_languages',
  'job_types',
] as const;

export function missingChatFields(f: OnboardingChatFields): string[] {
  const miss: string[] = [];
  if (!f.target_job_title?.trim()) miss.push('target_job_title');
  if (!normalizeExperience(f.experience_level)) miss.push('experience_level');
  if (!normalizeSearchStatus(f.job_search_status)) miss.push('job_search_status');
  if (!f.preferred_regions?.length) miss.push('preferred_regions');
  if (!f.country?.trim()) miss.push('country');
  if (!f.preferred_languages?.length) miss.push('preferred_languages');
  if (!f.job_types?.length) miss.push('job_types');
  return miss;
}

export function isChatReady(f: OnboardingChatFields): boolean {
  return missingChatFields(f).length === 0;
}

export function followUpReply(f: OnboardingChatFields): string {
  const miss = missingChatFields(f);
  if (miss.length === 0) {
    return "Thanks — I have everything I need. Pick a plan below to start matching.";
  }
  switch (miss[0]) {
    case 'target_job_title':
      return 'What role are you targeting? (e.g. Senior Software Engineer, Product Manager)';
    case 'experience_level':
      return "What's your experience level — entry, junior, mid, senior, lead, or executive?";
    case 'job_search_status':
      return 'How actively are you looking — actively looking, open to offers, or casually browsing?';
    case 'country':
      return 'Which country are you based in or targeting? (e.g. Kenya, Nigeria, remote-friendly)';
    case 'preferred_regions':
      return 'Which regions should we search? (Anywhere, Africa, Europe, North America, …)';
    case 'preferred_languages':
      return 'Which languages should job posts be in? (English, French, Swahili, …)';
    case 'job_types':
      return 'What employment types work for you? (Full-time, Part-time, Contract, Freelance, Internship)';
    default:
      return 'Could you share a bit more about the role and where you want to work?';
  }
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
  if (overlay.preferred_regions?.length)
    out.preferred_regions = unique(overlay.preferred_regions);
  if (overlay.preferred_timezones?.length)
    out.preferred_timezones = unique(overlay.preferred_timezones);
  if (overlay.preferred_languages?.length)
    out.preferred_languages = unique(overlay.preferred_languages);
  if (overlay.job_types?.length) out.job_types = unique(overlay.job_types);
  if (overlay.country?.trim()) out.country = normalizeCountry(overlay.country);
  if (overlay.extra_info?.trim()) out.extra_info = overlay.extra_info.trim();

  // Sensible defaults once we know something about the candidate.
  if (!out.job_search_status && out.target_job_title) {
    out.job_search_status = 'actively_looking';
  }
  if (!out.preferred_languages?.length && (out.target_job_title || (out.extra_info?.length ?? 0) > 80)) {
    out.preferred_languages = ['English'];
  }
  if (!out.preferred_regions?.length && out.country) {
    out.preferred_regions = regionForCountry(out.country);
  }
  if (!out.job_types?.length && out.target_job_title) {
    out.job_types = ['Full-time'];
  }
  return out;
}

export function heuristicExtract(msg: string): OnboardingChatFields {
  const low = msg.toLowerCase();
  const f: OnboardingChatFields = {};

  // Experience (prefer longer / more specific first)
  if (/\b(executive|director|vp|c-level|cxo)\b/i.test(msg)) f.experience_level = 'executive';
  else if (/\b(lead|staff|principal)\b/i.test(msg)) f.experience_level = 'lead';
  else if (/\b(senior|sr\.?)\b/i.test(msg)) f.experience_level = 'senior';
  else if (/\b(mid[- ]?level|intermediate)\b/i.test(msg)) f.experience_level = 'mid';
  else if (/\b(junior|jr\.?)\b/i.test(msg)) f.experience_level = 'junior';
  else if (/\b(entry[- ]?level|intern|graduate|new grad)\b/i.test(msg)) f.experience_level = 'entry';
  else if (/\b(\d+)\+?\s*years?\b/i.test(msg)) {
    const m = msg.match(/\b(\d+)\+?\s*years?\b/i);
    const n = m ? parseInt(m[1]!, 10) : 0;
    if (n >= 10) f.experience_level = 'lead';
    else if (n >= 5) f.experience_level = 'senior';
    else if (n >= 2) f.experience_level = 'mid';
    else f.experience_level = 'junior';
  }

  if (/\bactively\s+(looking|seeking)\b/i.test(msg)) f.job_search_status = 'actively_looking';
  else if (/\bopen\s+to\s+(offers?|opportunities)\b/i.test(msg)) f.job_search_status = 'open_to_offers';
  else if (/\b(casually|just browsing)\b/i.test(msg)) f.job_search_status = 'casually_browsing';

  const types: string[] = [];
  if (/\bfull[-\s]?time\b/i.test(msg)) types.push('Full-time');
  if (/\bpart[-\s]?time\b/i.test(msg)) types.push('Part-time');
  if (/\bcontract(or|ing)?\b/i.test(msg)) types.push('Contract');
  if (/\bfreelance\b/i.test(msg)) types.push('Freelance');
  if (/\bintern(ship)?\b/i.test(msg)) types.push('Internship');
  // Remote is a work mode — map to Full-time if nothing else said, but keep Remote as type if used alone
  if (/\bremote\b/i.test(msg) && types.length === 0) types.push('Full-time');
  else if (/\bremote\b/i.test(msg) && !types.includes('Full-time')) {
    /* keep existing types; remote preference is implied by regions/geo */
  }
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
  for (const [re, code] of countryHints) {
    if (re.test(msg)) {
      f.country = code;
      break;
    }
  }

  // Salary: "$80k", "KES 200000", "80000 USD"
  const sal = msg.match(
    /(?:(USD|EUR|GBP|KES|NGN|ZAR|GHS|AED|INR)\s*)?\$?\s*([\d,]+)\s*([kK])?(?:\s*[-–to]+\s*\$?\s*([\d,]+)\s*([kK])?)?(?:\s*(USD|EUR|GBP|KES|NGN|ZAR|GHS|AED|INR))?/
  );
  if (sal) {
    const cur = (sal[1] || sal[6] || 'USD').toUpperCase();
    const n1 = parseMoney(sal[2], sal[3]);
    const n2 = sal[4] ? parseMoney(sal[4], sal[5]) : n1;
    if (n1 > 0) {
      f.currency = cur;
      f.salary_min = Math.min(n1, n2);
      f.salary_max = Math.max(n1, n2);
    }
  }

  f.target_job_title = guessTitle(msg);
  f.extra_info = msg.slice(0, 2000);
  return f;
}

/** Run a local chat turn (no network). */
export function localChatTurn(
  message: string,
  draft: OnboardingChatFields
): OnboardingChatResponse {
  const extracted = heuristicExtract(message);
  const extra =
    draft.extra_info && draft.extra_info.trim()
      ? `${draft.extra_info.trim()}\n\n${message.trim()}`
      : message.trim();
  const fields = mergeChatFields({ ...draft, extra_info: extra }, extracted);
  const missing = missingChatFields(fields);
  return {
    reply: followUpReply(fields),
    fields,
    missing,
    ready: missing.length === 0,
  };
}

function parseMoney(num: string | undefined, k: string | undefined): number {
  if (!num) return 0;
  let n = parseFloat(num.replace(/,/g, ''));
  if (Number.isNaN(n)) return 0;
  if (k) n *= 1000;
  return n;
}

function guessTitle(msg: string): string {
  // Prefer identity phrases ("I am a …") over "looking for …" which often
  // matches "actively looking for full-time roles".
  const markers = [
    /i(?:'m| am) (?:a |an )?([^\n.,;]{3,80})/i,
    /work(?:ing)? as (?:a |an )?([^\n.,;]{3,80})/i,
    /role as (?:a |an )?([^\n.,;]{3,80})/i,
    /target(?:ing)? (?:role|title|job)(?: is|:)?\s*([^\n.,;]{3,80})/i,
    /position:\s*([^\n.,;]{3,80})/i,
    /title:\s*([^\n.,;]{3,80})/i,
    /seeking (?:a |an )?([^\n.,;]{3,80})/i,
    // "looking for a product manager" but not "actively looking for full-time"
    /(?:^|[^\w])looking for (?:a |an )([^\n.,;]{3,80})/i,
  ];
  for (const re of markers) {
    const m = msg.match(re);
    if (m?.[1]) {
      const t = cleanTitle(m[1]);
      if (t && !isJunkTitle(t)) return t;
    }
  }

  // CV-style: first non-empty line that looks like a job title (not a name-only line)
  const lines = msg.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
  for (const line of lines.slice(0, 8)) {
    if (line.length < 4 || line.length > 80) continue;
    if (/@|\.com|phone|email|http/i.test(line)) continue;
    if (/^(curriculum|resume|cv|profile|summary|objective)\b/i.test(line)) continue;
    // Prefer lines with job-ish words
    if (
      /\b(engineer|developer|designer|manager|analyst|director|officer|specialist|consultant|architect|scientist|nurse|teacher|accountant|marketer|product|sales)\b/i.test(
        line
      )
    ) {
      return cleanTitle(line) || line;
    }
  }
  return '';
}

function cleanTitle(s: string): string {
  let t = s.trim();
  // strip trailing "based in …" / "with N years" / "in Kenya"
  t = t.replace(/\s+based\b.*$/i, '');
  t = t.replace(/\s+with\s+\d+.*$/i, '');
  t = t.replace(/\s+in\s+[A-Za-z][A-Za-z\s]{1,40}$/i, '');
  t = t.replace(/\s*,.*$/i, '');
  t = t.replace(/\s+/g, ' ').trim();
  if (t.length < 2 || t.length > 80) return '';
  if (/^(a|an|the|looking|seeking)$/i.test(t)) return '';
  return t;
}

function isJunkTitle(s: string): boolean {
  return /^(full[-\s]?time|part[-\s]?time|remote|contract|roles?|jobs?|opportunities)\b/i.test(
    s
  );
}

function normalizeExperience(s?: string): string {
  switch ((s || '').toLowerCase().trim()) {
    case 'entry':
    case 'entry-level':
    case 'intern':
    case 'internship':
    case 'graduate':
      return 'entry';
    case 'junior':
    case 'jr':
      return 'junior';
    case 'mid':
    case 'mid-level':
    case 'middle':
    case 'intermediate':
      return 'mid';
    case 'senior':
    case 'sr':
      return 'senior';
    case 'lead':
    case 'staff':
    case 'principal':
      return 'lead';
    case 'executive':
    case 'director':
    case 'vp':
    case 'c-level':
    case 'cxo':
      return 'executive';
    default:
      return '';
  }
}

function normalizeSearchStatus(s?: string): string {
  switch ((s || '').toLowerCase().trim()) {
    case 'actively_looking':
    case 'actively looking':
    case 'active':
      return 'actively_looking';
    case 'open_to_offers':
    case 'open to offers':
    case 'open':
      return 'open_to_offers';
    case 'casually_browsing':
    case 'casually browsing':
    case 'browsing':
      return 'casually_browsing';
    default:
      return '';
  }
}

function normalizeCountry(s: string): string {
  const t = s.trim();
  if (t.length === 2) return t.toUpperCase();
  const aliases: Record<string, string> = {
    kenya: 'KE',
    uganda: 'UG',
    nigeria: 'NG',
    ghana: 'GH',
    'south africa': 'ZA',
    'united states': 'US',
    usa: 'US',
    'united kingdom': 'GB',
    uk: 'GB',
    germany: 'DE',
    france: 'FR',
    india: 'IN',
    philippines: 'PH',
    brazil: 'BR',
    rwanda: 'RW',
    tanzania: 'TZ',
    ethiopia: 'ET',
    egypt: 'EG',
    morocco: 'MA',
  };
  return aliases[t.toLowerCase()] ?? t;
}

function regionForCountry(cc: string): string[] {
  const c = cc.toUpperCase();
  const africa = new Set([
    'KE',
    'UG',
    'NG',
    'GH',
    'ZA',
    'RW',
    'TZ',
    'ET',
    'EG',
    'MA',
    'SN',
    'CI',
    'CM',
  ]);
  if (africa.has(c)) return ['Africa'];
  if (['US', 'CA', 'MX'].includes(c)) return ['North America'];
  if (['GB', 'DE', 'FR', 'NL', 'IE', 'ES', 'IT', 'SE', 'NO', 'CH'].includes(c)) return ['Europe'];
  if (['IN', 'PH', 'SG', 'JP', 'CN', 'AE', 'SA'].includes(c)) return ['Asia'];
  if (['BR', 'AR', 'CL', 'CO'].includes(c)) return ['South America'];
  if (['AU', 'NZ'].includes(c)) return ['Oceania'];
  return ['Anywhere'];
}

function unique(inArr: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const s of inArr) {
    const t = s.trim();
    if (!t) continue;
    const k = t.toLowerCase();
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(t);
  }
  return out;
}

// silence unused if tree-shaken differently
void REQUIRED;
