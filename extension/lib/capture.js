// Per-source capture: read cookies from chrome.cookies for the
// domains declared in a manifest entry, plus the user-agent header,
// and pack into the wire shape expected by
// POST /candidates/me/sessions/:source_type.
//
// We do NOT capture localStorage or sessionStorage in v1. Those live
// on the page side and require a content script per origin; for the
// HTTP-form replay path BrighterMonday uses, cookies are sufficient.
// When a future source requires storage capture, add a content-script
// path keyed off the manifest's storage_keys list.

export async function captureForSource(manifestEntry) {
  const cookies = await collectCookies(manifestEntry.cookie_domains || []);
  if (cookies.length === 0) {
    return null;
  }
  if (!hasRequiredCookies(manifestEntry.required_cookies, cookies)) {
    return null;
  }
  return {
    captured_at: new Date().toISOString(),
    user_agent: navigator.userAgent,
    cookies,
    headers: {
      "Accept-Language": navigator.language || "en",
    },
    storage: {},
  };
}

async function collectCookies(domains) {
  const out = [];
  for (const domain of domains) {
    const list = await chrome.cookies.getAll({ domain });
    for (const c of list) {
      out.push({
        name: c.name,
        value: c.value,
        domain: c.domain,
        path: c.path,
        expires: c.expirationDate
          ? new Date(c.expirationDate * 1000).toISOString()
          : undefined,
        http_only: c.httpOnly,
        secure: c.secure,
        same_site: c.sameSite || "",
      });
    }
  }
  return out;
}

function hasRequiredCookies(required, cookies) {
  if (!required || required.length === 0) return true;
  const have = new Set(cookies.map((c) => c.name));
  return required.every((r) => have.has(r));
}

// detectLoggedIn fires the optional probe declared in the manifest.
// Returns true when the probe responds with the expected status, false
// otherwise. Network failures are treated as "not logged in" — better
// to drop a capture than upload an anonymous session.
export async function detectLoggedIn(probe) {
  if (!probe) return true;
  try {
    const res = await fetch(probe.url, {
      method: probe.method || "GET",
      credentials: "include",
      cache: "no-store",
    });
    return res.status === (probe.expect_status || 200);
  } catch {
    return false;
  }
}
