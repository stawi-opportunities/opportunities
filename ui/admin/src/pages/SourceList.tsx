import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  listDiscoveredSources,
  listSources,
  rescoreSource,
  SOURCE_STATUSES,
  SOURCE_TYPES,
  type AdminSource,
  type SourceListItem,
} from "@/api/admin-client";
import {
  Button,
  Card,
  ErrorBlock,
  LoadingSkeleton,
  StatusBadge,
  useToast,
} from "@/components/ui";

const PAGE_SIZE = 25;

function healthBadge(score: number) {
  if (score >= 0.7)
    return <StatusBadge variant="success" label={score.toFixed(2)} size="sm" />;
  if (score >= 0.4)
    return <StatusBadge variant="warning" label={score.toFixed(2)} size="sm" />;
  return <StatusBadge variant="error" label={score.toFixed(2)} size="sm" />;
}

function statusBadge(status: string) {
  switch (status) {
    case "active":
      return <StatusBadge variant="success" label="active" size="sm" />;
    case "verified":
      return <StatusBadge variant="success" label="verified" size="sm" />;
    case "paused":
    case "pending":
    case "verifying":
      return <StatusBadge variant="warning" label={status} size="sm" />;
    case "rejected":
    case "disabled":
    case "blocked":
      return <StatusBadge variant="error" label={status} size="sm" />;
    default:
      return <StatusBadge variant="neutral" label={status} size="sm" />;
  }
}

type Tab = "all" | "discovered";

export function SourceList() {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [tab, setTab] = useState<Tab>("all");
  const [page, setPage] = useState(0);
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [type, setType] = useState("");

  const list = useQuery({
    queryKey: ["sources", page, status, type],
    queryFn: () =>
      listSources({
        limit: PAGE_SIZE,
        offset: page * PAGE_SIZE,
        status: status || undefined,
        type: type || undefined,
      }),
    refetchInterval: 30_000,
    enabled: tab === "all",
  });

  const discovered = useQuery({
    queryKey: ["sources-discovered"],
    queryFn: () => listDiscoveredSources(200),
    refetchInterval: 30_000,
    enabled: tab === "discovered",
  });

  const rescore = useMutation({
    mutationFn: (id: string) => rescoreSource(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["sources"] });
      toast("Score recomputed", { type: "success" });
    },
    onError: (e: Error) => {
      toast(`Rescore failed: ${e.message}`, { type: "error" });
    },
  });

  const rows: Array<SourceListItem | AdminSource> = useMemo(() => {
    if (tab === "discovered") return discovered.data?.sources ?? [];
    return list.data?.sources ?? [];
  }, [tab, list.data, discovered.data]);

  const filtered = useMemo(() => {
    if (!search.trim()) return rows;
    const q = search.toLowerCase();
    return rows.filter(
      (s) =>
        s.id.toLowerCase().includes(q) ||
        s.type.toLowerCase().includes(q) ||
        (s.country || "").toLowerCase().includes(q) ||
        (s.name || "").toLowerCase().includes(q) ||
        (s.base_url || "").toLowerCase().includes(q),
    );
  }, [rows, search]);

  const totalPages =
    tab === "all" && list.data
      ? Math.ceil(list.data.total / PAGE_SIZE)
      : 1;

  const isLoading = tab === "all" ? list.isLoading : discovered.isLoading;
  const error = tab === "all" ? list.error : discovered.error;

  if (isLoading) return <LoadingSkeleton type="table-row" rows={10} />;
  if (error)
    return (
      <ErrorBlock message="Failed to load sources" detail={String(error)} />
    );

  return (
    <div>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          marginBottom: "1rem",
          gap: "0.75rem",
          flexWrap: "wrap",
        }}
      >
        <div>
          <h1 style={{ margin: 0 }}>Sources</h1>
          <p
            style={{
              margin: "0.25rem 0 0",
              fontSize: "0.85rem",
              color: "var(--c-text-secondary)",
            }}
          >
            Onboard boards, verify, approve, install recipes, and crawl.
          </p>
        </div>
        <Link to="/sources/new">
          <Button>Add source</Button>
        </Link>
      </div>

      <div
        style={{
          display: "flex",
          gap: "0.5rem",
          marginBottom: "0.75rem",
          flexWrap: "wrap",
          alignItems: "center",
        }}
      >
        <Button
          variant={tab === "all" ? "primary" : "outline"}
          size="sm"
          onClick={() => setTab("all")}
        >
          All sources
        </Button>
        <Button
          variant={tab === "discovered" ? "primary" : "outline"}
          size="sm"
          onClick={() => setTab("discovered")}
        >
          Onboarding queue
          {discovered.data
            ? ` (${discovered.data.count})`
            : ""}
        </Button>
        {tab === "all" && (
          <>
            <select
              aria-label="Filter status"
              value={status}
              onChange={(e) => {
                setStatus(e.target.value);
                setPage(0);
              }}
              style={{
                padding: "0.3rem 0.5rem",
                borderRadius: "var(--radius-sm)",
                border: "1px solid var(--c-border)",
              }}
            >
              <option value="">All statuses</option>
              {SOURCE_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
            <select
              aria-label="Filter type"
              value={type}
              onChange={(e) => {
                setType(e.target.value);
                setPage(0);
              }}
              style={{
                padding: "0.3rem 0.5rem",
                borderRadius: "var(--radius-sm)",
                border: "1px solid var(--c-border)",
              }}
            >
              <option value="">All types</option>
              {SOURCE_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </>
        )}
        <input
          type="search"
          placeholder="Search id, type, url…"
          aria-label="Search sources"
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setPage(0);
          }}
          style={{
            padding: "0.35rem 0.6rem",
            fontSize: "0.85rem",
            border: "1px solid var(--c-border)",
            borderRadius: "var(--radius-sm)",
            width: 240,
            marginLeft: "auto",
          }}
        />
      </div>

      {filtered.length === 0 ? (
        <Card>
          <p role="status" style={{ color: "var(--c-text-secondary)", margin: 0 }}>
            No sources match.{" "}
            <Link to="/sources/new">Add a source</Link> to start crawling.
          </p>
        </Card>
      ) : (
        <Card padding={false}>
          <table>
            <thead>
              <tr>
                <th>Source</th>
                <th>Type</th>
                <th>Status</th>
                <th>Country</th>
                <th>Health</th>
                <th>Score</th>
                <th>Next crawl</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((s) => (
                <tr key={s.id}>
                  <td>
                    <Link to={`/sources/${encodeURIComponent(s.id)}`}>
                      <strong>{s.name || s.id.slice(0, 12) + "…"}</strong>
                    </Link>
                    <div
                      style={{
                        fontSize: "0.75rem",
                        color: "var(--c-text-secondary)",
                        maxWidth: 280,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {s.base_url}
                    </div>
                    {s.needs_tuning && (
                      <StatusBadge
                        variant="warning"
                        label="needs tuning"
                        size="sm"
                      />
                    )}
                  </td>
                  <td>
                    <code>{s.type}</code>
                  </td>
                  <td>{statusBadge(s.status)}</td>
                  <td>{s.country || "—"}</td>
                  <td>{healthBadge(s.health_score ?? 0)}</td>
                  <td>
                    {typeof s.score === "number" ? s.score.toFixed(2) : "—"}
                  </td>
                  <td style={{ whiteSpace: "nowrap", fontSize: "0.82rem" }}>
                    {s.next_crawl_at
                      ? new Date(s.next_crawl_at).toLocaleString()
                      : "—"}
                  </td>
                  <td>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={rescore.isPending}
                      onClick={() => rescore.mutate(s.id)}
                    >
                      Rescore
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {tab === "all" && (
            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                padding: "0.75rem 1rem",
                borderTop: "1px solid var(--c-border)",
                fontSize: "0.85rem",
              }}
            >
              <span>
                {list.data?.total ?? 0} total · page {page + 1}
                {totalPages ? ` / ${totalPages}` : ""}
              </span>
              <div style={{ display: "flex", gap: "0.5rem" }}>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page <= 0}
                  onClick={() => setPage((p) => p - 1)}
                >
                  Prev
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page + 1 >= totalPages}
                  onClick={() => setPage((p) => p + 1)}
                >
                  Next
                </Button>
              </div>
            </div>
          )}
        </Card>
      )}
    </div>
  );
}
