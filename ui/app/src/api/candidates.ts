import { getConfig } from "@/utils/config";
import { getAuthRuntime } from "@stawi/auth-runtime";

function join(base: string, path: string): string {
  const b = base.replace(/\/$/, "");
  const p = path.startsWith("/") ? path : "/" + path;
  return b + p;
}

async function bearer(): Promise<Record<string, string>> {
  try {
    const token = await getAuthRuntime().getAccessToken();
    return token ? { Authorization: `Bearer ${token}` } : {};
  } catch {
    return {};
  }
}

/** POST /candidates/onboard — creates or updates the candidate profile. */
export async function submitOnboarding(payload: unknown): Promise<Response> {
  const url = join(getConfig().candidatesAPIURL, "/candidates/onboard");
  return fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", ...(await bearer()) },
    body: JSON.stringify(payload),
    credentials: "include",
  });
}
