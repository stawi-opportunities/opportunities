import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import Cascade from "./Cascade";
import { useAuth } from "@/providers/AuthProvider";
import { fetchCandidate } from "@/api/candidates";

// LocaleShard is the per-country landing page component. The Hugo
// layout at ui/layouts/locale/shard.html renders a mount element
// carrying `data-locale-country` + `data-locale-languages` (derived
// from ui/data/locale_shards.yaml at build time). Those attributes
// pre-seed the Cascade so the feed requests the right slice without
// waiting on navigator.languages or CF-IPCountry inference.
//
// The signed-in user's preferences still apply on top — if they've
// set preferred_countries = [US], the preferred tier shows US jobs
// and this page's home country (KE, say) shifts to the "local" tier.
export default function LocaleShard() {
  const mount = useMemo(
    () => document.getElementById("mount-locale-shard"),
    [],
  );
  const country = (mount?.getAttribute("data-locale-country") ?? "").toUpperCase();
  const langsCSV = mount?.getAttribute("data-locale-languages") ?? "";

  const languages = useMemo(
    () =>
      langsCSV
        .split(",")
        .map((s) => s.trim().toLowerCase())
        .filter(Boolean),
    [langsCSV],
  );

  // Write the visitor-locale meta tag so any other locale-aware code
  // on the page (share buttons, future analytics) picks up the same
  // context without re-reading data-*.
  useEffect(() => {
    if (!country) return;
    const meta = document.createElement("meta");
    meta.name = "visitor-locale";
    meta.content = JSON.stringify({ country, languages });
    document.head.appendChild(meta);
    return () => { meta.remove(); };
  }, [country, languages]);

  const auth = useAuth();
  const profile = useQuery({
    queryKey: ["candidate-profile"],
    queryFn: fetchCandidate,
    enabled: auth.state === "authenticated",
    staleTime: 5 * 60_000,
  });
  const preferredCountries = useMemo(
    () => splitCSV(profile.data?.preferred_countries),
    [profile.data?.preferred_countries],
  );
  const preferredLanguages = useMemo(
    () => splitCSV(profile.data?.languages),
    [profile.data?.languages],
  );

  // Forward the shard's country/langs into the Cascade via explicit
  // overrides.  Cascade already reads getVisitorLocale() by default,
  // but on a shard page the page *is* the locale; we don't want
  // navigator.languages or URL params second-guessing the landing.
  return (
    <ShardStatusBanner country={country} languages={languages}>
      <Cascade
        filters={{ sort: "recent" }}
        preferredCountries={preferredCountries}
        preferredLanguages={preferredLanguages}
        tierLimit={25}
        overrideCountry={country}
        overrideLanguages={languages}
      />
    </ShardStatusBanner>
  );
}

function ShardStatusBanner({
  country,
  languages,
  children,
}: {
  country: string;
  languages: string[];
  children: React.ReactNode;
}) {
  const [dismissed, setDismissed] = useState(false);
  if (dismissed || !country) return <>{children}</>;
  return (
    <>
      <p
        role="status"
        className="mb-4 flex items-center justify-between rounded-md bg-emerald-50 px-4 py-2 text-sm text-emerald-800"
      >
        <span>
          Showing {country} jobs{languages.length ? ` in ${languages.join(", ")}` : ""}.
        </span>
        <button
          type="button"
          onClick={() => setDismissed(true)}
          className="ml-4 text-xs font-medium text-emerald-900 underline hover:text-emerald-950"
        >
          Dismiss
        </button>
      </p>
      {children}
    </>
  );
}

function splitCSV(csv: string | undefined | null): string[] {
  if (!csv) return [];
  return csv
    .split(/[,;]/)
    .map((s) => s.trim())
    .filter(Boolean);
}
