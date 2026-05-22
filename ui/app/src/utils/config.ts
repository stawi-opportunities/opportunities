// Runtime configuration reader.
//
// Values flow: hugo.toml params → <meta name="site-params"> in head.html →
// this module → React components. That single path keeps per-environment
// config out of the JS bundle — one build runs against staging, production,
// or local dev by just changing HUGO_PARAMS_* at build time.

export interface SiteConfig {
  /** Jobs API origin, may include a /jobs path prefix. */
  apiURL: string;
  /** Candidates / profile API origin (unified api.stawi.org root). */
  candidatesAPIURL: string;
  /** R2 public content origin for OpportunitySnapshot JSON. */
  contentOrigin: string;
  /** OIDC issuer (Ory Hydra). */
  oidcIssuer: string;
  /** Hydra client_id string for this environment. */
  oidcClientID: string;
  /** Partition XID from Thesa; passed as installation_id in auth URLs. */
  oidcInstallationID: string;
  /** Registered redirect URI — must match Hydra's client record exactly. */
  oidcRedirectURI: string;

  /** GA4 measurement ID, e.g. "G-XXXXXXXXXX". Empty disables analytics. */
  gaMeasurementId: string;
}

const DEFAULTS: SiteConfig = {
  apiURL: "https://api.stawi.org/jobs",
  candidatesAPIURL: "https://api.stawi.org",
  contentOrigin: "https://opportunities-data.stawi.org",
  oidcIssuer: "https://oauth2.stawi.org",
  oidcClientID: "d7is2kspf2t7cl19qlpg",
  oidcInstallationID: "d7gi6lkpf2t67dlsqrhg",
  oidcRedirectURI:
    typeof window !== "undefined"
      ? `${window.location.origin}/auth/callback/`
      : "http://localhost:5170/auth/callback/",
  // GA4 measurement ID — empty default so the gtag.js script tag is
  // skipped in head.html unless HUGO_PARAMS_gaMeasurementId is set at
  // build time. IDs are public ("G-XXXXXXXXXX") so they can live in
  // hugo.toml directly when needed.
  gaMeasurementId: "",
};

let cached: SiteConfig | null = null;

/** Read the config once per page load and cache it. */
export function getConfig(): SiteConfig {
  if (cached) return cached;
  cached = loadFromMeta();
  return cached;
}

function loadFromMeta(): SiteConfig {
  if (typeof document === "undefined") return DEFAULTS;
  const el = document.querySelector<HTMLMetaElement>(
    'meta[name="site-params"]',
  );
  if (!el) return DEFAULTS;
  try {
    const parsed = JSON.parse(el.content) as Partial<SiteConfig>;
    return { ...DEFAULTS, ...stripEmpty(parsed) };
  } catch {
    return DEFAULTS;
  }
}

function stripEmpty<T extends object>(obj: T): Partial<T> {
  const out: Partial<T> = {};
  for (const [k, v] of Object.entries(obj) as [keyof T, unknown][]) {
    if (v !== "" && v != null) {
      out[k] = v as T[keyof T];
    }
  }
  return out;
}
