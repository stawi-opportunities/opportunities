// MV3 service worker. Two long-lived responsibilities:
//
//   1. Refresh the source-auth manifest from the API on a slow rotation
//      so the supported-domains list stays current without an extension
//      update. (New domains still require updating host_permissions in
//      manifest.json — those can't be added at runtime in MV3.)
//
//   2. Watch chrome.cookies.onChanged for any domain that matches a
//      manifest entry. When a change lands and the optional probe says
//      "logged in", debounce and upload the capture.
//
// All work goes through ./lib/api.js, which transparently refreshes
// the access token on 401.

import { fetchSourceManifest, uploadCapture, HttpError } from "./lib/api.js";
import { captureForSource, detectLoggedIn } from "./lib/capture.js";
import { getManifest, setManifest, getTokens } from "./lib/storage.js";

const MANIFEST_ALARM = "refresh-manifest";
const MANIFEST_REFRESH_MIN = 60; // 1 hour
const DEBOUNCE_MS = 1500;

const pendingUploads = new Map(); // source_type → timeout id

chrome.runtime.onInstalled.addListener(() => {
  chrome.alarms.create(MANIFEST_ALARM, { periodInMinutes: MANIFEST_REFRESH_MIN });
  void refreshManifest();
});

chrome.runtime.onStartup.addListener(() => {
  void refreshManifest();
});

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === MANIFEST_ALARM) {
    void refreshManifest();
  }
});

chrome.cookies.onChanged.addListener((change) => {
  if (change.cause === "evicted" || change.removed) return;
  void onCookieChange(change.cookie);
});

async function refreshManifest() {
  const tokens = await getTokens();
  if (!tokens?.access_token) {
    // Not paired yet — nothing to fetch.
    return;
  }
  try {
    const resp = await fetchSourceManifest();
    await setManifest(resp.sources || []);
  } catch (err) {
    console.warn("stawi: manifest refresh failed", err);
  }
}

async function onCookieChange(cookie) {
  const manifest = await getManifest();
  if (!manifest?.sources) return;
  const entry = manifest.sources.find((s) =>
    (s.cookie_domains || []).some((d) => domainMatches(d, cookie.domain)),
  );
  if (!entry) return;
  // Debounce — a single login fires many cookie writes.
  if (pendingUploads.has(entry.source_type)) {
    clearTimeout(pendingUploads.get(entry.source_type));
  }
  pendingUploads.set(
    entry.source_type,
    setTimeout(() => {
      pendingUploads.delete(entry.source_type);
      void tryCapture(entry);
    }, DEBOUNCE_MS),
  );
}

// tryCapture returns `true` on a successful upload, or one of these
// string reasons on failure: "not_logged_in" | "no_cookies" |
// "needs_repair" | "upload_failed". The popup uses these to render a
// concrete status next to each source rather than a generic spinner.
async function tryCapture(entry) {
  const loggedIn = await detectLoggedIn(entry.detect_logged_in);
  if (!loggedIn) return "not_logged_in";
  const cap = await captureForSource(entry);
  if (!cap) return "no_cookies";
  try {
    await uploadCapture(entry.source_type, cap);
    console.info("stawi: uploaded capture for", entry.source_type);
    return true;
  } catch (err) {
    if (err instanceof HttpError && err.status === 401) {
      // Refresh dance in api.js already cleared tokens — popup will
      // surface "needs re-pair".
      return "needs_repair";
    }
    console.warn("stawi: capture upload failed", err);
    return "upload_failed";
  }
}

// Cookie domains in chrome are stored with a leading dot for the
// "all subdomains" form. We accept either form in the manifest.
function domainMatches(pattern, cookieDomain) {
  const p = pattern.startsWith(".") ? pattern.slice(1) : pattern;
  const d = cookieDomain.startsWith(".") ? cookieDomain.slice(1) : cookieDomain;
  return d === p || d.endsWith("." + p);
}

// Popup → background messages.
chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
  if (msg?.type === "stawi:capture-now") {
    (async () => {
      const manifest = await getManifest();
      const entry = (manifest?.sources || []).find(
        (s) => s.source_type === msg.source_type,
      );
      if (!entry) {
        sendResponse({ ok: false, error: "unknown source" });
        return;
      }
      // tryCapture handles the detectLoggedIn gate + cookie capture +
      // upload. Surface the result so the popup can show "Captured ✓"
      // vs "Not logged in" rather than a generic spinner.
      const result = await tryCapture(entry);
      sendResponse({ ok: result === true, reason: result === true ? undefined : result });
    })();
    return true; // async sendResponse
  }
  if (msg?.type === "stawi:refresh-manifest") {
    (async () => {
      await refreshManifest();
      sendResponse({ ok: true });
    })();
    return true;
  }
  if (msg?.type === "stawi:status") {
    // Per-source status snapshot for the popup. Runs the same
    // detect_logged_in probe the auto-capture path uses, plus
    // optionally pulls the last-captured-at timestamp from the
    // server's session list. Cheap to call from popup mount.
    (async () => {
      const manifest = await getManifest();
      const sources = manifest?.sources || [];
      const results = await Promise.all(
        sources.map(async (s) => {
          const loggedIn = await detectLoggedIn(s.detect_logged_in);
          return { source_type: s.source_type, logged_in: loggedIn };
        }),
      );
      sendResponse({ ok: true, statuses: results });
    })();
    return true;
  }
  return false;
});
