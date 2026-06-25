// Token + manifest storage wrappers. Everything lives in
// chrome.storage.local (per-profile, persists across browser
// restarts). We never use localStorage — content scripts in arbitrary
// pages can read it.
//
// Schema:
//   tokens:    { candidate_id, access_token, refresh_token,
//                access_expires_at, refresh_expires_at }
//   manifest:  { fetched_at, sources: [ExtensionView, ...] }

const TOKENS_KEY = "stawi:tokens";
const MANIFEST_KEY = "stawi:manifest";

export async function getTokens() {
  const out = await chrome.storage.local.get(TOKENS_KEY);
  return out[TOKENS_KEY] ?? null;
}

export async function setTokens(tokens) {
  await chrome.storage.local.set({ [TOKENS_KEY]: tokens });
}

export async function clearTokens() {
  await chrome.storage.local.remove(TOKENS_KEY);
}

export async function getManifest() {
  const out = await chrome.storage.local.get(MANIFEST_KEY);
  return out[MANIFEST_KEY] ?? null;
}

export async function setManifest(sources) {
  await chrome.storage.local.set({
    [MANIFEST_KEY]: { fetched_at: Date.now(), sources },
  });
}
