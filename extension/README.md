# Stawi AutoApply Extension (MV3)

Captures authenticated session cookies from job boards the user has
explicitly connected, and forwards them to the Stawi candidates API
for replay-based auto-apply.

The extension is intentionally thin: no UI for credentials, no
heuristics about login state — everything it knows about which sites
it supports comes from `GET /sources/auth-manifest`, served by
`apps/matching`.

## Layout

```
extension/
  manifest.json          MV3 manifest with the static host_permissions
  background.js          service worker — fetches the source manifest,
                         observes cookie changes, posts captures
  popup/popup.html       click-the-icon popup with pair + status
  popup/popup.js         pair-flow handlers
  popup/popup.css        minimal styling
  lib/api.js             API client (pairings, sessions endpoints)
  lib/storage.js         chrome.storage.local wrappers for tokens
  lib/capture.js         per-source cookie/header capture logic
```

`host_permissions` in `manifest.json` ships with `brightermonday.co.ke`
hard-coded so the extension passes Chrome Web Store review without
asking for `<all_urls>`. Adding a new source = (a) ship a new YAML in
`definitions/source-auth/` AND (b) ship a new extension version with
the host added.

## Local dev install

1. `make ui-build` (no extension build step; pure JS — Chrome loads it raw)
2. In Chrome: `chrome://extensions` → toggle **Developer mode** →
   **Load unpacked** → pick this `extension/` directory.
3. Click the Stawi icon, paste the pairing code from your Stawi
   dashboard (Phase 6 wires the UI for that).
4. Visit <https://www.brightermonday.co.ke/login>, sign in, then click
   **Connect this account** in the extension popup.

## Configuration

Set the API origin in `lib/api.js` (`API_ORIGIN` constant) before
building for production. Defaults to `https://api.stawi.org`.
