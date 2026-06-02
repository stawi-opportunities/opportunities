// Admin sub-app runtime config reader.
//
// Mirrors ui/app/src/utils/config.ts — values flow:
//   hugo.toml params → <meta name="site-params"> in admin layout → here.
// One build runs against staging/production/local dev by just changing
// HUGO_PARAMS_* at Hugo build time.

export interface SiteConfig {
  /** Admin trace API origin (bare api.stawi.org root). The admin
   *  /admin/* routes live on the api service alongside /jobs/*. */
  candidatesAPIURL: string;
  /** OIDC issuer (Ory Hydra). */
  oidcIssuer: string;
  /** Hydra client_id string for this environment. */
  oidcClientID: string;
  /** Partition XID from Thesa; passed as installation_id in auth URLs. */
  oidcInstallationID: string;
  /** Registered redirect URI — must match Hydra's client record exactly. */
  oidcRedirectURI: string;
}

const DEFAULTS: SiteConfig = {
  candidatesAPIURL: 'https://api.stawi.org',
  oidcIssuer: 'https://oauth2.stawi.org',
  oidcClientID: 'd7is2kspf2t7cl19qlpg',
  oidcInstallationID: 'd7gi6lkpf2t67dlsqrhg',
  oidcRedirectURI:
    typeof window !== 'undefined'
      ? `${window.location.origin}/auth/callback/`
      : 'http://localhost:5170/auth/callback/',
};

let cached: SiteConfig | null = null;

export function getConfig(): SiteConfig {
  if (cached) return cached;
  cached = loadFromMeta();
  return cached;
}

function loadFromMeta(): SiteConfig {
  if (typeof document === 'undefined') return DEFAULTS;
  const el = document.querySelector<HTMLMetaElement>('meta[name="site-params"]');
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
    if (v !== '' && v != null) {
      out[k] = v as T[keyof T];
    }
  }
  return out;
}
