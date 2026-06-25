import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  listSources,
  rescoreSource,
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
    case "paused":
      return <StatusBadge variant="warning" label="paused" size="sm" />;
    case "error":
      return <StatusBadge variant="error" label="error" size="sm" />;
    default:
      return <StatusBadge variant="neutral" label={status} size="sm" />;
  }
}

export function SourceList() {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [page, setPage] = useState(0);
  const [search, setSearch] = useState("");

  const { data, isLoading, error } = useQuery({
    queryKey: ["sources", page],
    queryFn: () => listSources(PAGE_SIZE, page * PAGE_SIZE),
    refetchInterval: 30_000,
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

  const filtered = useMemo(() => {
    if (!data?.sources) return [];
    if (!search.trim()) return data.sources;
    const q = search.toLowerCase();
    return data.sources.filter(
      (s) =>
        s.id.toLowerCase().includes(q) ||
        s.type.toLowerCase().includes(q) ||
        s.country.toLowerCase().includes(q),
    );
  }, [data, search]);

  const totalPages = data ? Math.ceil(data.total / PAGE_SIZE) : 0;

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
        <h1 style={{ margin: 0 }}>Sources</h1>
        <input
          type="text"
          placeholder="Search by ID, type, or country…"
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
            width: 260,
            outline: "none",
          }}
        />
      </div>

      {filtered.length === 0 ? (
        <Card>
          <p
            role="status"
            style={{ color: "var(--c-text-secondary)", margin: 0 }}
          >
            {search
              ? "No sources match your search."
              : "No sources configured."}
          </p>
        </Card>
      ) : (
        <Card padding={false}>
          <div style={{ overflowX: "auto" }}>
            <table>
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Type</th>
                  <th>Status</th>
                  <th>Country</th>
                  <th>Health</th>
                  <th>Digest</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((s: SourceListItem) => (
                  <tr key={s.id}>
                    <td>
                      <Link to={`/sources/${encodeURIComponent(s.id)}`}>
                        {s.id}
                      </Link>
                    </td>
                    <td>{s.type}</td>
                    <td>{statusBadge(s.status)}</td>
                    <td>{s.country}</td>
                    <td>
                      {typeof s.health_score === "number"
                        ? healthBadge(s.health_score)
                        : "—"}
                    </td>
                    <td>
                      <Link to={`/seeds/${encodeURIComponent(s.id)}/digest`}>
                        digest →
                      </Link>
                    </td>
                    <td>
                      <Button
                        size="sm"
                        variant="ghost"
                        loading={
                          rescore.isPending && rescore.variables === s.id
                        }
                        onClick={() => rescore.mutate(s.id)}
                      >
                        Rescore
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}

      {data && totalPages > 1 && (
        <div
          style={{
            display: "flex",
            justifyContent: "center",
            alignItems: "center",
            gap: "0.75rem",
            marginTop: "1rem",
            fontSize: "0.85rem",
          }}
        >
          <Button
            size="sm"
            variant="outline"
            disabled={page === 0}
            aria-label="Previous page"
            aria-disabled={page === 0}
            onClick={() => setPage((p) => p - 1)}
          >
            Previous
          </Button>
          <span role="status" style={{ color: "var(--c-text-secondary)" }}>
            Page {page + 1} of {totalPages} ({data.total} total)
          </span>
          <Button
            size="sm"
            variant="outline"
            disabled={page >= totalPages - 1}
            aria-label="Next page"
            aria-disabled={page >= totalPages - 1}
            onClick={() => setPage((p) => p + 1)}
          >
            Next
          </Button>
        </div>
      )}
    </div>
  );
}
