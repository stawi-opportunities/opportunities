import { useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  clearCheckpoint,
  fetchAdminJSON,
  listCheckpoints,
  type CheckpointRow,
  type SourceTraceResponse,
} from "@/api/admin-client";
import {
  Button,
  Card,
  ConfirmDialog,
  ErrorBlock,
  LoadingSkeleton,
  StatusBadge,
  useToast,
} from "@/components/ui";

interface StatCardProps {
  label: string;
  value: string | number;
  color?: string;
}

function StatCard({ label, value, color }: StatCardProps) {
  return (
    <div
      style={{
        background: "var(--c-surface)",
        border: "1px solid var(--c-border)",
        borderRadius: "var(--radius-md)",
        padding: "0.75rem 1rem",
        textAlign: "center",
        minWidth: 100,
      }}
    >
      <div
        style={{
          fontSize: "1.4rem",
          fontWeight: 700,
          color: color ?? "var(--c-text)",
        }}
      >
        {value}
      </div>
      <div
        style={{
          fontSize: "0.78rem",
          color: "var(--c-text-secondary)",
          marginTop: "0.15rem",
        }}
      >
        {label}
      </div>
    </div>
  );
}

export function SourceTrace() {
  const { id } = useParams<{ id: string }>();
  const queryClient = useQueryClient();
  const { toast } = useToast();

  const [cpToReset, setCpToReset] = useState<CheckpointRow | null>(null);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  const toggle = (key: string) =>
    setCollapsed((p) => ({ ...p, [key]: !p[key] }));

  const { data, isLoading, error } = useQuery({
    queryKey: ["source-trace", id],
    queryFn: () =>
      fetchAdminJSON<SourceTraceResponse>(
        `/admin/trace/sources/${encodeURIComponent(id ?? "")}?since=24h`,
      ),
    enabled: !!id,
    refetchInterval: 30_000,
  });

  const { data: cpData } = useQuery({
    queryKey: ["checkpoints", id],
    queryFn: () => listCheckpoints(id),
    enabled: !!id,
  });

  const clearCp = useMutation({
    mutationFn: (row: CheckpointRow) =>
      clearCheckpoint(row.source_id, row.connector_type),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["checkpoints", id] });
      toast("Checkpoint cleared", { type: "success" });
      setCpToReset(null);
    },
    onError: (e: Error) => {
      toast(`Failed: ${e.message}`, { type: "error" });
      setCpToReset(null);
    },
  });

  if (!id) return <ErrorBlock message="Missing source ID" />;
  if (isLoading) return <LoadingSkeleton type="card" />;
  if (error)
    return (
      <ErrorBlock
        message="Failed to load source trace"
        detail={String(error)}
      />
    );
  if (!data) return null;

  const { source, summary, recent_crawls } = data;
  const checkpoints = cpData?.checkpoints ?? [];

  return (
    <div>
      <div style={{ marginBottom: "1.25rem" }}>
        <h1 style={{ margin: 0 }}>
          {source.id}{" "}
          <span
            style={{
              fontWeight: 400,
              fontSize: "0.9rem",
              color: "var(--c-text-secondary)",
            }}
          >
            ({source.type})
          </span>
        </h1>
        <p
          style={{
            margin: "0.35rem 0 0",
            color: "var(--c-text-secondary)",
            fontSize: "0.88rem",
          }}
        >
          {source.base_url} · {source.country} · status{" "}
          <StatusBadge
            variant={
              source.status === "active"
                ? "success"
                : source.status === "paused"
                  ? "warning"
                  : "error"
            }
            label={source.status}
            size="sm"
          />
          · health score <code>{source.health_score.toFixed(2)}</code>
        </p>
        {source.next_crawl_at && (
          <p
            style={{
              margin: "0.25rem 0 0",
              color: "var(--c-text-secondary)",
              fontSize: "0.82rem",
            }}
          >
            Next crawl at {new Date(source.next_crawl_at).toLocaleString()}
          </p>
        )}
      </div>

      {/* 24-hour summary */}
      <div style={{ marginBottom: "1.25rem" }}>
        <SectionHeader
          label="24-hour summary"
          sectionKey="summary"
          collapsed={collapsed}
          onToggle={toggle}
        />
        {!collapsed["summary"] && (
          <div
            id="section-summary"
            style={{
              display: "flex",
              gap: "0.75rem",
              flexWrap: "wrap",
              marginTop: "0.75rem",
            }}
          >
            <StatCard label="Crawl jobs" value={summary.crawl_jobs} />
            <StatCard
              label="Failed"
              value={summary.crawl_jobs_failed}
              color="var(--c-danger)"
            />
            <StatCard label="Raw payloads" value={summary.raw_payloads} />
            <StatCard
              label="Emitted"
              value={summary.variants_emitted}
              color="var(--c-info)"
            />
            <StatCard
              label="Published"
              value={summary.variants_published}
              color="var(--c-success)"
            />
            <StatCard
              label="Rejected"
              value={summary.variants_rejected}
              color="var(--c-danger)"
            />
          </div>
        )}
        {!collapsed["summary"] &&
          Object.keys(summary.rejection_reasons).length > 0 && (
            <div style={{ marginTop: "0.75rem" }}>
              <h3 style={{ fontSize: "0.9rem", margin: "0 0 0.35rem" }}>
                Rejection reasons
              </h3>
              <ul
                style={{
                  margin: 0,
                  paddingLeft: "1.25rem",
                  fontSize: "0.85rem",
                }}
              >
                {Object.entries(summary.rejection_reasons).map(
                  ([reason, count]) => (
                    <li key={reason}>
                      <code>{reason}</code> × {count}
                    </li>
                  ),
                )}
              </ul>
            </div>
          )}
      </div>

      {/* Iterator checkpoints */}
      <div style={{ marginBottom: "1.25rem" }}>
        <SectionHeader
          label="Iterator checkpoints"
          sectionKey="checkpoints"
          collapsed={collapsed}
          onToggle={toggle}
        />
        {!collapsed["checkpoints"] && (
          <div id="section-checkpoints">
            {checkpoints.length === 0 ? (
              <p
                style={{
                  color: "var(--c-text-secondary)",
                  fontSize: "0.85rem",
                  marginTop: "0.5rem",
                }}
              >
                No active checkpoints — last crawl completed cleanly.
              </p>
            ) : (
              <Card padding={false}>
                <table>
                  <thead>
                    <tr>
                      <th>Connector</th>
                      <th>Page</th>
                      <th>Last URL</th>
                      <th>Updated</th>
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    {checkpoints.map((c) => (
                      <tr key={`${c.source_id}/${c.connector_type}`}>
                        <td>
                          <code>{c.connector_type}</code>
                        </td>
                        <td>{c.page_idx}</td>
                        <td
                          style={{
                            maxWidth: 280,
                            overflow: "hidden",
                            textOverflow: "ellipsis",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {c.last_url ?? ""}
                        </td>
                        <td style={{ whiteSpace: "nowrap" }}>
                          {new Date(c.last_checkpoint_at).toLocaleString()}
                        </td>
                        <td>
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => setCpToReset(c)}
                          >
                            Reset
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </Card>
            )}
          </div>
        )}
      </div>

      {/* Recent crawls */}
      <div>
        <SectionHeader
          label="Recent crawls"
          sectionKey="crawls"
          collapsed={collapsed}
          onToggle={toggle}
        />
        {!collapsed["crawls"] && (
          <div id="section-crawls">
            {recent_crawls.length === 0 ? (
              <p
                style={{
                  color: "var(--c-text-secondary)",
                  fontSize: "0.85rem",
                  marginTop: "0.5rem",
                }}
              >
                No crawls in the window.
              </p>
            ) : (
              <Card padding={false}>
                <table>
                  <thead>
                    <tr>
                      <th>Scheduled</th>
                      <th>Status</th>
                      <th>Found</th>
                      <th>Stored</th>
                      <th>Raw</th>
                      <th>Duration</th>
                      <th>Error</th>
                    </tr>
                  </thead>
                  <tbody>
                    {recent_crawls.map((c) => (
                      <tr key={c.crawl_job_id}>
                        <td style={{ whiteSpace: "nowrap" }}>
                          {new Date(c.scheduled_at).toLocaleString()}
                        </td>
                        <td>{c.status}</td>
                        <td>{c.jobs_found}</td>
                        <td>{c.jobs_stored}</td>
                        <td>{c.raw_payloads}</td>
                        <td>{c.duration_ms} ms</td>
                        <td>{c.error_code ?? ""}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </Card>
            )}
          </div>
        )}
      </div>

      <ConfirmDialog
        open={!!cpToReset}
        title="Reset checkpoint"
        message={`Clear the checkpoint for ${cpToReset?.source_id}/${cpToReset?.connector_type}? The next crawl will start from page 1.`}
        confirmLabel="Reset"
        variant="danger"
        busy={clearCp.isPending}
        onConfirm={() => {
          if (cpToReset) clearCp.mutate(cpToReset);
        }}
        onCancel={() => setCpToReset(null)}
      />
    </div>
  );
}

function SectionHeader({
  label,
  sectionKey,
  collapsed,
  onToggle,
}: {
  label: string;
  sectionKey: string;
  collapsed: Record<string, boolean>;
  onToggle: (key: string) => void;
}) {
  const isCollapsed = !!collapsed[sectionKey];
  const contentId = `section-${sectionKey}`;
  const handleKey = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      onToggle(sectionKey);
    }
  };
  return (
    <h2
      onClick={() => onToggle(sectionKey)}
      onKeyDown={handleKey}
      tabIndex={0}
      role="button"
      aria-expanded={!isCollapsed}
      aria-controls={contentId}
      style={{
        fontSize: "1.05rem",
        margin: 0,
        cursor: "pointer",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: "0.4rem",
      }}
    >
      <span
        style={{
          fontSize: "0.75rem",
          color: "var(--c-text-secondary)",
          transition: "transform 0.2s",
          display: "inline-block",
          transform: isCollapsed ? "rotate(-90deg)" : undefined,
        }}
      >
        ▼
      </span>
      {label}
    </h2>
  );
}
