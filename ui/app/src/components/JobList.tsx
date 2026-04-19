import { useMemo } from "react";
import Cascade from "./Cascade";
import { useAuth } from "@/providers/AuthProvider";
import { useQuery } from "@tanstack/react-query";
import { fetchCandidate } from "@/api/candidates";

/** /jobs/ — tiered discovery feed. Default sort is recency; filters
 *  and text search live on /search/. */
export default function JobList() {
  const auth = useAuth();
  const authed = auth.state === "authenticated";

  // Pull the candidate profile only if authenticated — its country /
  // language preferences drive the "preferred" tier. One lightweight
  // request that queries the candidates service's /me/profile.
  const profile = useQuery({
    queryKey: ["candidate-profile"],
    queryFn: fetchCandidate,
    enabled: authed,
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

  return (
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
      <h1 className="text-3xl font-bold">All jobs</h1>
      <p className="mt-2 text-gray-600">
        Most relevant first, based on your location and language.
      </p>
      <Cascade
        filters={{ sort: "recent" }}
        preferredCountries={preferredCountries}
        preferredLanguages={preferredLanguages}
        tierLimit={25}
      />
    </div>
  );
}

function splitCSV(csv: string | undefined | null): string[] {
  if (!csv) return [];
  return csv
    .split(/[,;]/)
    .map((s) => s.trim())
    .filter(Boolean);
}
