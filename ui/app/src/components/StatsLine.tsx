import { useEffect, useState } from "react";
import { getConfig } from "@/utils/config";

/**
 * StatsLine renders a compact "👁 4,291 views • ✉ 142 applies" line
 * underneath the per-kind body. Reads from the public
 * /opportunities/{slug}/stats endpoint (no auth — pure-public Valkey
 * counters). Renders nothing while loading or on failure so a Valkey
 * blip never breaks the detail page.
 */

interface Stats {
  views_total: number;
  views_24h: number;
  applies_total: number;
  applies_24h: number;
}

export default function StatsLine({ slug }: { slug: string }) {
  const [stats, setStats] = useState<Stats | null>(null);

  useEffect(() => {
    if (!slug) return;
    const ctrl = new AbortController();
    fetch(`${getConfig().apiURL}/opportunities/${encodeURIComponent(slug)}/stats`, {
      signal: ctrl.signal,
    })
      .then((r) => (r.ok ? r.json() : null))
      .then((data: Stats | null) => {
        if (data) setStats(data);
      })
      .catch(() => {
        // Silent — analytics line is non-critical.
      });
    return () => ctrl.abort();
  }, [slug]);

  if (!stats) return null;
  // Hide entirely when both counters are zero — a fresh listing
  // showing "0 views" is noise, not a useful signal.
  if (stats.views_total === 0 && stats.applies_total === 0) return null;

  return (
    <p className="mt-3 text-xs text-gray-500">
      <span aria-hidden>👁</span> {fmt(stats.views_total)} views
      {" • "}
      <span aria-hidden>✉</span> {fmt(stats.applies_total)} applies
    </p>
  );
}

function fmt(n: number): string {
  if (n < 1000) return n.toString();
  if (n < 10_000) return `${(n / 1000).toFixed(1)}k`;
  return `${Math.round(n / 1000)}k`;
}
