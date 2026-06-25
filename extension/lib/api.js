// API client for the Stawi candidates service.
//
// The extension speaks two distinct auth flavours:
//   - Pair redeem: unauthenticated (code IS the auth)
//   - Everything else (sessions, refresh): Stawi access token via Bearer
//
// Access tokens are short-lived (15m default). If a call returns 401
// we transparently exchange the refresh token for a new access token
// and retry once. A failed refresh clears local state and surfaces a
// "needs re-pair" condition to the popup.

import { getTokens, setTokens, clearTokens } from "./storage.js";

// Local-dev override: when the extension is loaded unpacked we want
// it to talk to the local matching service on :8082, not production.
// Flip this back to https://api.stawi.org before packaging for the
// Chrome Web Store / AMO.
const API_ORIGIN = "http://localhost:8082";

class HttpError extends Error {
  constructor(status, body) {
    super(`HTTP ${status}`);
    this.status = status;
    this.body = body;
  }
}

async function jsonFetch(path, init = {}, opts = {}) {
  const url = API_ORIGIN + path;
  const res = await fetch(url, {
    ...init,
    headers: { Accept: "application/json", ...(init.headers || {}) },
  });
  const text = await res.text();
  let body = null;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = text;
    }
  }
  if (!res.ok) {
    if (res.status === 401 && opts.allowRefresh) {
      const refreshed = await refreshAccessToken();
      if (refreshed) {
        return jsonFetch(path, init, { ...opts, allowRefresh: false });
      }
    }
    throw new HttpError(res.status, body);
  }
  return body;
}

async function authedFetch(path, init = {}) {
  const tokens = await getTokens();
  if (!tokens?.access_token) {
    throw new HttpError(401, { error: { code: "no_token", message: "extension not paired" } });
  }
  return jsonFetch(
    path,
    {
      ...init,
      headers: {
        ...(init.headers || {}),
        Authorization: `Bearer ${tokens.access_token}`,
      },
    },
    { allowRefresh: true },
  );
}

async function refreshAccessToken() {
  const tokens = await getTokens();
  if (!tokens?.refresh_token) {
    return false;
  }
  try {
    const body = await jsonFetch("/pairings/refresh", {
      method: "POST",
      headers: { Authorization: `Bearer ${tokens.refresh_token}` },
    });
    await setTokens({
      ...tokens,
      access_token: body.access_token,
      access_expires_at: Date.now() + body.access_expires_in * 1000,
    });
    return true;
  } catch (err) {
    // Refresh dead → drop everything; popup will prompt for re-pair.
    await clearTokens();
    return false;
  }
}

// ── public surface ─────────────────────────────────────────────────

export async function redeemPairingCode(code) {
  const body = await jsonFetch("/pairings/redeem", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code: code.trim().toUpperCase() }),
  });
  await setTokens({
    candidate_id: body.candidate_id,
    access_token: body.access_token,
    refresh_token: body.refresh_token,
    access_expires_at: Date.now() + body.access_expires_in * 1000,
    refresh_expires_at: Date.now() + body.refresh_expires_in * 1000,
  });
  return body;
}

export async function fetchSourceManifest() {
  // Manifest endpoint is auth-required (stawi-token) — go through
  // authedFetch so the refresh dance kicks in on stale tokens.
  return authedFetch("/sources/auth-manifest");
}

export async function uploadCapture(sourceType, capture) {
  return authedFetch(`/candidates/me/sessions/${encodeURIComponent(sourceType)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(capture),
  });
}

export { HttpError };
