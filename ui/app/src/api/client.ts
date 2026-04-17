import { getConfig } from "@/utils/config";

// Shared HTTP helpers. Two axes of base URL exist:
//   apiURL           = https://api.stawi.org/jobs (search / jobs)
//   candidatesAPIURL = https://api.stawi.org      (profile / candidates)
// Both are deliberately distinct so routes collide cleanly at the gateway.

/** Join a base URL (which may contain a path prefix) with a relative path. */
function join(base: string, path: string): string {
  const b = base.replace(/\/$/, "");
  const p = path.startsWith("/") ? path : "/" + path;
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

export type ApiParams = Record<
  string,
  string | number | boolean | null | undefined
>;

/** GET against the jobs API (api.stawi.org/jobs). */
export async function jobsApiGet<T>(
  path: string,
  params: ApiParams = {},
): Promise<T> {
  const url = new URL(join(getConfig().apiURL, path));
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    url.searchParams.set(k, String(v));
  }
  const res = await fetch(url.toString(), { credentials: "omit" });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, `${path}: HTTP ${res.status}`, body);
  }
  return (await res.json()) as T;
}
