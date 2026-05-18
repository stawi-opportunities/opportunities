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

async function tryCapture(entry) {
  const loggedIn = await detectLoggedIn(entry.detect_logged_in);
  if (!loggedIn) return;
  const cap = await captureForSource(entry);
  if (!cap) return;
  try {
    await uploadCapture(entry.source_type, cap);
    console.info("stawi: uploaded capture for", entry.source_type);
  } catch (err) {
    if (err instanceof HttpError && err.status === 401) {
      // Refresh dance in api.js already cleared tokens — nothing more
      // to do here; the popup will surface the "needs re-pair" state.
      return;
    }
    console.warn("stawi: capture upload failed", err);
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
      await tryCapture(entry);
      sendResponse({ ok: true });
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
  return false;
});
