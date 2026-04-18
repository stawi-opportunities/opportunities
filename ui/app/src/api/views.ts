import { getConfig } from "@/utils/config";

/**
 * Beacon a job-view event at the jobs API. The API records the view
 * with a server-side profile attribution (from the JWT) and kicks off
 * a throttled reachability probe against the job's apply URL.
 *
 * Prefers `navigator.sendBeacon` because the HTTP response isn't
 * interesting — we want the request queued even if the user navigates
 * away before it completes. Falls back to fetch with keepalive: true
 * in environments where sendBeacon is restricted.
 *
 * Never throws; this is strictly fire-and-forget telemetry.
 */
export function pingJobView(slug: string): void {
  if (!slug || typeof window === "undefined") return;
  const url = `${getConfig().apiURL}/jobs/${encodeURIComponent(slug)}/view`;

  try {
    if (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function") {
      // Empty blob body — the server only needs slug (from the URL)
      // and request headers (JWT / CF-IPCountry / User-Agent). A Blob
      // with text/plain is what sendBeacon accepts without a
      // `preflight`-triggering Content-Type.
      const blob = new Blob([""], { type: "text/plain" });
      const ok = navigator.sendBeacon(url, blob);
      if (ok) return;
      // Fall through to fetch if the beacon queue was full / rejected.
    }
    void fetch(url, {
      method: "POST",
      keepalive: true,
      credentials: "include",
      headers: { "Content-Type": "text/plain" },
      body: "",
    }).catch(() => {
      // Silent — the endpoint is best-effort.
    });
  } catch {
    // Belt-and-braces — we never want telemetry to crash the page.
  }
}
