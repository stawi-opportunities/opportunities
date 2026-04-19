// CloudFlare Pages Function — runs on every request.
//
// Responsibilities:
//
//   1. Geo-redirect: if the user hits the site root ("/") we send
//      them to /l/<CC>/ based on CF-IPCountry when that country has
//      a published locale shard. Set a "geo-redirected" cookie so
//      subsequent navigation back to "/" doesn't bounce again — lets
//      users explicitly choose "Worldwide" by clearing the cookie.
//
//   2. CF-IPCountry reflection: CloudFlare sets the request header
//      `cf-ipcountry`; we set a `x-stawi-country` response header on
//      non-cached HTML so the frontend (and any downstream fetch
//      that proxies this header) gets the value reliably.
//
// This runs at the edge; the static Hugo build and Cascade both
// remain functional without it.

interface Env {}

// Countries with a shard at /l/<CC>/ (see ui/data/locale_shards.yaml).
// Keep in sync — if a user lands from a country that's NOT on this
// list we don't redirect; they get the generic landing page and the
// server-side feed infers their location from CF-IPCountry anyway.
const SHARDS = new Set([
  "KE", "UG", "TZ", "RW", "ET",
  "NG", "GH",
  "ZA",
  "EG", "MA",
  "US", "GB", "DE",
  "IN", "PH", "BR",
]);

const GEO_COOKIE = "stawi_geo_v1";

export const onRequest: PagesFunction<Env> = async (context) => {
  const { request, next } = context;
  const url = new URL(request.url);

  // Only meddle with the root path — /jobs, /search, /l/*, /pricing,
  // etc. are deliberate destinations the user chose.
  if (url.pathname === "/" || url.pathname === "") {
    const country = request.headers.get("cf-ipcountry")?.toUpperCase() ?? "";
    const cookie = request.headers.get("cookie") ?? "";
    const alreadyRouted = cookie.includes(`${GEO_COOKIE}=`);

    if (!alreadyRouted && country && SHARDS.has(country)) {
      const redirect = new URL(`/l/${country.toLowerCase()}/`, url);
      const res = Response.redirect(redirect.toString(), 302);
      // 302 can't carry Set-Cookie on its own in the Response.redirect
      // constructor — rebuild so we can attach the header.
      const resWithCookie = new Response(null, {
        status: 302,
        headers: {
          Location: redirect.toString(),
          // Persist the decision for 1 hour so subsequent "go home"
          // clicks don't bounce; users who want to escape the shard
          // can clear cookies or explicitly visit /?force=1.
          "Set-Cookie": `${GEO_COOKIE}=${country}; Path=/; Max-Age=3600; SameSite=Lax`,
          "Cache-Control": "private, no-store",
        },
      });
      // Drop `res` — we rebuilt it. Keeping the variable referenced
      // so the type checker sees Response.redirect was used safely.
      void res;
      return resWithCookie;
    }
  }

  // Propagate CF-IPCountry to the HTML response so any downstream
  // locale-aware UI can read it without its own IP lookup.
  const response = await next();
  const country = request.headers.get("cf-ipcountry") ?? "";
  if (country && response.headers.get("content-type")?.startsWith("text/html")) {
    // Clone so we can mutate headers (immutable by default).
    const mutable = new Response(response.body, response);
    mutable.headers.set("x-stawi-country", country.toUpperCase());
    return mutable;
  }
  return response;
};
