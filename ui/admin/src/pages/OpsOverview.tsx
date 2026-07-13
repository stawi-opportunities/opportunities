import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { getOpsOverview } from "@/api/admin-client";
import {
  Card,
  ErrorBlock,
  LoadingSkeleton,
  StatusBadge,
} from "@/components/ui";
import { RejectionChart } from "@/components/RejectionChart";

function Metric({
  label,
  value,
  hint,
}: {
  label: string;
  value: number | string;
  hint?: string;
}) {
  return (
    <Card>
      <div
        style={{
          fontSize: "0.75rem",
          fontWeight: 600,
          textTransform: "uppercase",
          letterSpacing: "0.04em",
          color: "var(--c-text-secondary)",
          marginBottom: "0.35rem",
        }}
      >
        {label}
      </div>
      <div style={{ fontSize: "1.75rem", fontWeight: 700, lineHeight: 1.1 }}>
        {value}
      </div>
      {hint && (
        <div
          style={{
            marginTop: "0.35rem",
            fontSize: "0.8rem",
            color: "var(--c-text-secondary)",
          }}
        >
          {hint}
        </div>
      )}
    </Card>
  );
}

export function OpsOverview() {
  const { data, isLoading, error, dataUpdatedAt } = useQuery({
    queryKey: ["ops-overview"],
    queryFn: getOpsOverview,
    refetchInterval: 15_000,
  });

  if (isLoading) return <LoadingSkeleton type="card" />;
  if (error)
    return (
      <ErrorBlock message="Failed to load ops overview" detail={String(error)} />
    );
  if (!data) return null;

  const c = data.counts;

  return (
    <div>
      <div
        style={{
          display: "flex",
          alignItems: "baseline",
          justifyContent: "space-between",
          marginBottom: "1.25rem",
          gap: "1rem",
          flexWrap: "wrap",
        }}
      >
        <div>
          <h1 style={{ margin: 0 }}>Crawl ops</h1>
          <p
            style={{
              margin: "0.25rem 0 0",
              color: "var(--c-text-secondary)",
              fontSize: "0.88rem",
            }}
          >
            Pipeline health at a glance. Auto-refreshes every 15s.
            {dataUpdatedAt
              ? ` Last: ${new Date(dataUpdatedAt).toLocaleTimeString()}`
              : null}
          </p>
        </div>
        <div style={{ display: "flex", gap: "0.5rem" }}>
          <Link to="/jobs">Browse jobs</Link>
          <Link to="/rejections">Rejections</Link>
        </div>
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(160px, 1fr))",
          gap: "0.75rem",
          marginBottom: "1.5rem",
        }}
      >
        <Metric label="Active jobs" value={c.active_jobs} />
        <Metric label="Hidden jobs" value={c.hidden_jobs} />
        <Metric
          label="Sources"
          value={`${c.sources_active}/${c.sources_total}`}
          hint={`${c.sources_paused} paused`}
        />
        <Metric label="Queue pending" value={c.queue_pending} />
        <Metric label="Queue processing" value={c.queue_processing} />
        <Metric label="Queue dead" value={c.queue_dead} />
        <Metric label="Published 24h" value={c.published_24h} />
        <Metric label="Rejected 24h" value={c.rejected_24h} />
        <Metric
          label="Crawl jobs 24h"
          value={c.crawl_jobs_24h}
          hint={`${c.crawl_failed_24h} failed`}
        />
        <Metric label="Active runs" value={c.active_runs} />
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "minmax(0, 1fr) minmax(0, 1.4fr)",
          gap: "1rem",
          marginBottom: "1.5rem",
        }}
        className="ops-split"
      >
        <Card>
          <h2 style={{ marginTop: 0, fontSize: "1rem" }}>
            Rejection reasons (24h)
          </h2>
          {Object.keys(data.rejection_reasons).length === 0 ? (
            <p style={{ color: "var(--c-text-secondary)", margin: 0 }}>
              No rejections in the last 24 hours.
            </p>
          ) : (
            <RejectionChart reasons={data.rejection_reasons} />
          )}
        </Card>

        <Card padding={false}>
          <div style={{ padding: "1rem 1rem 0.5rem" }}>
            <h2 style={{ margin: 0, fontSize: "1rem" }}>Recently seen jobs</h2>
          </div>
          {data.recent.length === 0 ? (
            <p
              style={{
                color: "var(--c-text-secondary)",
                padding: "0 1rem 1rem",
              }}
            >
              No opportunities in the database yet. Seed sources and start a
              crawl.
            </p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Title</th>
                  <th>Kind</th>
                  <th>Country</th>
                  <th>Seen</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {data.recent.map((j) => (
                  <tr key={j.slug}>
                    <td>
                      <Link
                        to={`/opportunities/${encodeURIComponent(j.slug)}`}
                      >
                        {j.title || j.slug}
                      </Link>
                      {j.hidden && (
                        <>
                          {" "}
                          <StatusBadge
                            variant="warning"
                            label="hidden"
                            size="sm"
                          />
                        </>
                      )}
                    </td>
                    <td>
                      <code>{j.kind}</code>
                    </td>
                    <td>{j.country || "—"}</td>
                    <td style={{ whiteSpace: "nowrap" }}>
                      {new Date(j.last_seen_at).toLocaleString()}
                    </td>
                    <td>
                      <Link to={`/jobs?q=${encodeURIComponent(j.slug)}`}>
                        edit
                      </Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>
      </div>
    </div>
  );
}
