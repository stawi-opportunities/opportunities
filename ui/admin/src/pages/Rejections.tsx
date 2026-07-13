import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { listRejections } from "@/api/admin-client";
import {
  Card,
  ErrorBlock,
  LoadingSkeleton,
} from "@/components/ui";

function reasonsFromDetails(details: unknown): string {
  if (!details) return "—";
  if (typeof details === "string") {
    try {
      return reasonsFromDetails(JSON.parse(details));
    } catch {
      return details;
    }
  }
  if (typeof details === "object" && details !== null) {
    const d = details as Record<string, unknown>;
    if (Array.isArray(d.reasons) && d.reasons.length) {
      return d.reasons.map(String).join(", ");
    }
    if (typeof d.reason === "string" && d.reason) return d.reason;
    if (typeof d.detail === "string" && d.detail) return d.detail;
  }
  return JSON.stringify(details).slice(0, 120);
}

export function Rejections() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["rejections"],
    queryFn: () => listRejections(200),
    refetchInterval: 20_000,
  });

  if (isLoading) return <LoadingSkeleton type="table-row" rows={10} />;
  if (error)
    return (
      <ErrorBlock message="Failed to load rejections" detail={String(error)} />
    );

  return (
    <div>
      <div style={{ marginBottom: "1rem" }}>
        <h1 style={{ margin: 0 }}>Rejections</h1>
        <p
          style={{
            margin: "0.25rem 0 0",
            color: "var(--c-text-secondary)",
            fontSize: "0.88rem",
          }}
        >
          Most recent crawl-time rejects from{" "}
          <code>job_ingest_events</code> (structured accept gate).
        </p>
      </div>

      <Card padding={false}>
        <table>
          <thead>
            <tr>
              <th>When</th>
              <th>Source</th>
              <th>Variant</th>
              <th>Reasons</th>
            </tr>
          </thead>
          <tbody>
            {(data?.rejections ?? []).map((r, i) => (
              <tr key={`${r.variant_id}-${r.occurred_at}-${i}`}>
                <td style={{ whiteSpace: "nowrap" }}>
                  {new Date(r.occurred_at).toLocaleString()}
                </td>
                <td>
                  <Link to={`/sources/${encodeURIComponent(r.source_id)}`}>
                    {r.source_id}
                  </Link>
                </td>
                <td>
                  <Link to={`/variants/${encodeURIComponent(r.variant_id)}`}>
                    <code>{r.variant_id.slice(0, 12)}…</code>
                  </Link>
                </td>
                <td>
                  <code>{reasonsFromDetails(r.details)}</code>
                </td>
              </tr>
            ))}
            {(data?.rejections ?? []).length === 0 && (
              <tr>
                <td
                  colSpan={4}
                  style={{
                    padding: "1.5rem",
                    color: "var(--c-text-secondary)",
                  }}
                >
                  No rejections recorded yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </Card>
    </div>
  );
}
