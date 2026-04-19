// Per-user locale resolution for the tiered discovery feed.
//
// Sources, first-wins:
//   1. URL override: ?country=KE&lang=sw,en  (for debugging / sharing)
//   2. Hugo-baked meta tag: <meta name="visitor-locale">           (edge/CDN)
//   3. document.documentElement.lang + navigator.languages          (browser)
//
// Authenticated candidate preferences (preferred_countries /
// preferred_languages from CandidateProfile) land alongside the
// inferred values via applyPreferences() — the inferred country
// stays as the "local" tier anchor, preferences drive "preferred".

export interface VisitorLocale {
  /** ISO-3166 alpha-2, uppercase. Empty when unknown. */
  country: string;
  /** BCP-47 base subtags (en, sw, fr) — ordered most-preferred first.
   *  Empty array when we couldn't detect anything. */
  languages: string[];
}

export interface PreferredBoost {
  /** ISO-3166 alpha-2 list; overrides inferred country for the
   *  "preferred" tier. */
  countries: string[];
  /** BCP-47 base subtag list. */
  languages: string[];
}

let cached: VisitorLocale | null = null;

/** Read once per page load and cache. Call from components. */
export function getVisitorLocale(): VisitorLocale {
  if (cached) return cached;
  cached = resolve();
  return cached;
}

/** Test seam — lets unit tests reset the cache between runs. */
export function __resetLocaleCacheForTests(): void {
  cached = null;
}

function resolve(): VisitorLocale {
  if (typeof window === "undefined") return { country: "", languages: [] };

  // 1. URL override wins — useful for support/debug and for sharing
  //    a locale-specific link with someone in a different country.
  const qs = new URLSearchParams(window.location.search);
  const qCountry = (qs.get("country") ?? "").trim().toUpperCase();
  const qLang = parseLangList(qs.get("lang") ?? "");
  if (qCountry || qLang.length) {
    return {
      country: qCountry,
      languages: qLang.length ? qLang : detectLanguages(),
    };
  }

  // 2. Edge meta tag, if our CloudFlare worker / Hugo shard landed it.
  const meta = readMeta("visitor-locale");
  if (meta) {
    try {
      const parsed = JSON.parse(meta) as Partial<VisitorLocale>;
      const country = (parsed.country ?? "").toUpperCase();
      const languages = Array.isArray(parsed.languages)
        ? parsed.languages.map(baseTag).filter(Boolean)
        : [];
      if (country || languages.length) {
        return {
          country,
          languages: languages.length ? languages : detectLanguages(),
        };
      }
    } catch {
      /* fall through to browser inference */
    }
  }

  // 3. Browser inference.
  return { country: "", languages: detectLanguages() };
}

function detectLanguages(): string[] {
  if (typeof navigator === "undefined") return [];
  const raw = navigator.languages && navigator.languages.length
    ? navigator.languages
    : navigator.language
      ? [navigator.language]
      : [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const tag of raw) {
    const b = baseTag(tag);
    if (b && !seen.has(b)) {
      seen.add(b);
      out.push(b);
    }
    if (out.length >= 5) break;
  }
  return out;
}

function readMeta(name: string): string | null {
  const el = document.querySelector<HTMLMetaElement>(
    `meta[name="${CSS.escape(name)}"]`,
  );
  return el?.content.trim() || null;
}

/** Collapse a BCP-47 tag to its base subtag ("en-US" → "en"). */
export function baseTag(tag: string): string {
  const trimmed = tag.trim();
  if (!trimmed) return "";
  const sep = /[-_]/;
  const base = trimmed.split(sep, 1)[0] ?? "";
  return base.toLowerCase();
}

function parseLangList(csv: string): string[] {
  if (!csv) return [];
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of csv.split(",")) {
    const b = baseTag(raw);
    if (b && !seen.has(b)) {
      seen.add(b);
      out.push(b);
    }
  }
  return out;
}

/** Merge candidate preferences into visitor locale, producing the
 *  boost values the /api/feed endpoint expects. Preferences stay
 *  separate from inferred — they feed the "preferred" tier only. */
export function buildBoost(
  preferredCountriesCSV?: string | null,
  preferredLanguagesCSV?: string | null,
): PreferredBoost {
  return {
    countries: splitUpper(preferredCountriesCSV ?? ""),
    languages: parseLangList(preferredLanguagesCSV ?? ""),
  };
}

function splitUpper(csv: string): string[] {
  return csv
    .split(/[,;]/)
    .map((s) => s.trim().toUpperCase())
    .filter(Boolean);
}

/** Human label for a two-letter country code. Kept in sync with
 *  apps/api/cmd/tiered.go:countryDisplayName. */
export function countryLabel(cc: string): string {
  const up = cc.toUpperCase();
  const table: Record<string, string> = {
    KE: "Kenya",
    UG: "Uganda",
    TZ: "Tanzania",
    RW: "Rwanda",
    ET: "Ethiopia",
    NG: "Nigeria",
    GH: "Ghana",
    ZA: "South Africa",
    EG: "Egypt",
    MA: "Morocco",
    US: "the United States",
    GB: "the United Kingdom",
    CA: "Canada",
    DE: "Germany",
    IN: "India",
    AU: "Australia",
    PH: "the Philippines",
    BR: "Brazil",
  };
  return table[up] ?? up ?? "your region";
}
