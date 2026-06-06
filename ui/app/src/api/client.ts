import { getConfig } from '@/utils/config';

function join(base: string, path: string): string {
  const b = base.replace(/\/$/, '');
  const p = path.startsWith('/') ? path : '/' + path;
  return b + p;
}

export class ApiError extends Error {
  readonly status: number;
  readonly body?: string;
  constructor(status: number, message: string, body?: string) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

export type ApiParams = Record<string, string | number | boolean | null | undefined>;

<<<<<<< HEAD
=======
/** Maximum number of retries for transient 5xx errors. */
>>>>>>> 3f870c1 (feat: UI updates — auth flow, SignupCta, Nav, Search, Onboarding, and layout partials)
const MAX_RETRIES = 2;
const RETRY_BASE_MS = 100;

export async function jobsApiGet<T>(
  path: string,
  params: ApiParams = {},
): Promise<T> {
  const url = new URL(join(getConfig().apiURL, path));
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue;
    url.searchParams.set(k, String(v));
  }
<<<<<<< HEAD
=======

>>>>>>> 3f870c1 (feat: UI updates — auth flow, SignupCta, Nav, Search, Onboarding, and layout partials)
  let lastError!: ApiError;
  for (let attempt = 0; attempt <= MAX_RETRIES; attempt++) {
    if (attempt > 0) {
      await new Promise<void>((resolve) =>
        setTimeout(resolve, RETRY_BASE_MS * 2 ** (attempt - 1)),
      );
    }
    const res = await fetch(url.toString(), { credentials: "omit" });
    if (res.ok) return (await res.json()) as T;
    const body = await res.text().catch(() => "");
    lastError = new ApiError(res.status, `${path}: HTTP ${res.status}`, body);
    if (res.status < 500) throw lastError;
  }
  throw lastError;
}