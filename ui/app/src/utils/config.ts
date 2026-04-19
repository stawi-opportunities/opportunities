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
  /** R2 public content origin for JobSnapshot JSON. */
  contentOrigin: string;
  /** OIDC issuer (Ory Hydra). */
  oidcIssuer: string;
  /** Hydra client_id string for this environment. */
  oidcClientID: string;
  /** Partition XID from Thesa; passed as installation_id in auth URLs. */
  oidcInstallationID: string;
  /** Registered redirect URI — must match Hydra's client record exactly. */
  oidcRedirectURI: string;

  /** OpenObserve RUM ingest token (browser-only). Provision from OO UI. */
  rumClientToken: string;
  /** Logical app identifier registered in OpenObserve. */
  rumApplicationId: string;
  /** OpenObserve host:port (no scheme), e.g. "observe.stawi.org". */
  rumSite: string;
  /** OpenObserve organisation slug. */
  rumOrganization: string;
  /** Free-form environment tag for OO session filtering. */
  rumEnv: string;
  /** Service name reported on every RUM event. */
  rumService: string;
  /** Frontend version for OO session filtering. Wire to git-sha at build. */
  rumVersion: string;
}

const DEFAULTS: SiteConfig = {
  apiURL: "https://api.stawi.org/jobs",
  candidatesAPIURL: "https://api.stawi.org",
  contentOrigin: "https://job-repo.stawi.org",
  oidcIssuer: "https://oauth2.stawi.org",
  oidcClientID: "stawi-jobs-web-dev",
  oidcInstallationID: "d7gi6lkpf2t67dlsqrhg",
  oidcRedirectURI:
    typeof window !== "undefined"
      ? `${window.location.origin}/auth/callback/`
      : "http://localhost:5170/auth/callback/",
  // OpenObserve RUM — default to empty so the SDK stays silent unless
  // hugo.toml params (or HUGO_PARAMS_* env overrides at build time)
  // actually provide a client token. `rumSite` is public and can ship
  // as a default; the token cannot.
  rumClientToken: "",
  rumApplicationId: "stawi-jobs-web",
  rumSite: "observe.stawi.org",
  rumOrganization: "default",
  rumEnv: "production",
  rumService: "stawi-jobs-web",
  rumVersion: "0.0.0",
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
