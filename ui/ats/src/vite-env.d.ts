/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_ATS_DEV_HEADERS?: string;
  readonly VITE_OIDC_ISSUER?: string;
  readonly VITE_OIDC_CLIENT_ID?: string;
  readonly VITE_OIDC_INSTALLATION_ID?: string;
  readonly VITE_OIDC_REDIRECT_URI?: string;
  readonly VITE_API_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
