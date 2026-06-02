import { getConfig } from '@/utils/config';

// Shared HTTP helpers. Two axes of base URL exist:
//   apiURL           = https://api.stawi.org/jobs (search / jobs)
//   candidatesAPIURL = https://api.stawi.org      (profile / candidates)
// Both are deliberately distinct so routes collide cleanly at the gateway.

/** Join a base URL (which may contain a path prefix) with a relative path. */
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

/** Maximum number of retries for transient 5xx errors. */
const MAX_RETRIES = 2;
/** Base delay in ms — doubles on each retry (100 ms → 200 ms). */
const RETRY_BASE_MS = 100;

/** GET against the jobs API (api.stawi.org/jobs). Retries up to
 *  MAX_RETRIES times on 5xx with exponential back-off; fails fast on
 *  4xx so callers surface not-found / auth errors immediately. */
export async function jobsApiGet<T>(
  path: string,
  params: ApiParams = {},
): Promise<T> {
/** GET against the jobs API (api.stawi.org/jobs). */
export async function jobsApiGet<T>(path: string, params: ApiParams = {}): Promise<T> {
  const url = new URL(join(getConfig().apiURL, path));
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue;
    url.searchParams.set(k, String(v));
  }

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
    // Fail fast on client errors — retrying a 404 or 401 won't help.
    if (res.status < 500) throw lastError;
  const res = await fetch(url.toString(), { credentials: 'omit' });
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new ApiError(res.status, `${path}: HTTP ${res.status}`, body);
  }
  throw lastError;
}
