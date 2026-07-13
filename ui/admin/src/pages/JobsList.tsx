import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  getOpportunity,
  hideOpportunity,
  listOpportunities,
  patchOpportunity,
  unhideOpportunity,
  type AdminOpportunity,
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

export function JobsList() {
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const hidden = (params.get("hidden") as "" | "true" | "false") || "";
  const page = Math.max(0, parseInt(params.get("page") || "0", 10) || 0);
  const [selected, setSelected] = useState<string | null>(null);

  const queryClient = useQueryClient();
  const { toast } = useToast();

  const list = useQuery({
    queryKey: ["admin-jobs", q, hidden, page],
    queryFn: () =>
      listOpportunities({
        q: q || undefined,
        hidden: hidden || undefined,
        limit: PAGE_SIZE,
        offset: page * PAGE_SIZE,
      }),
    refetchInterval: 30_000,
  });

  const detail = useQuery({
    queryKey: ["admin-job", selected],
    queryFn: () => getOpportunity(selected!),
    enabled: !!selected,
  });

  const hideMut = useMutation({
    mutationFn: (slug: string) => hideOpportunity(slug, "operator_removed"),
    onSuccess: () => {
      toast("Job hidden from public serving", { type: "success" });
      queryClient.invalidateQueries({ queryKey: ["admin-jobs"] });
      queryClient.invalidateQueries({ queryKey: ["admin-job"] });
    },
    onError: (e: Error) => toast(e.message, { type: "error" }),
  });

  const unhideMut = useMutation({
    mutationFn: (slug: string) => unhideOpportunity(slug),
    onSuccess: () => {
      toast("Job restored to public serving", { type: "success" });
      queryClient.invalidateQueries({ queryKey: ["admin-jobs"] });
      queryClient.invalidateQueries({ queryKey: ["admin-job"] });
    },
    onError: (e: Error) => toast(e.message, { type: "error" }),
  });

  const clearFieldMut = useMutation({
    mutationFn: ({
      slug,
      clear_fields,
      clear_attributes,
    }: {
      slug: string;
      clear_fields?: string[];
      clear_attributes?: string[];
    }) => patchOpportunity(slug, { clear_fields, clear_attributes }),
    onSuccess: () => {
      toast("Field cleared", { type: "success" });
      queryClient.invalidateQueries({ queryKey: ["admin-job"] });
      queryClient.invalidateQueries({ queryKey: ["admin-jobs"] });
    },
    onError: (e: Error) => toast(e.message, { type: "error" }),
  });

  const totalPages = list.data
    ? Math.ceil(list.data.total / PAGE_SIZE)
    : 0;

  const setFilter = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    if (key !== "page") next.set("page", "0");
    setParams(next);
  };

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
        <h1 style={{ margin: 0 }}>Jobs</h1>
        <div style={{ display: "flex", gap: "0.5rem", flexWrap: "wrap" }}>
          <input
            type="search"
            placeholder="Search title, issuer, slug…"
            aria-label="Search jobs"
            defaultValue={q}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                setFilter("q", (e.target as HTMLInputElement).value.trim());
              }
            }}
            style={{
              padding: "0.4rem 0.6rem",
              border: "1px solid var(--c-border)",
              borderRadius: "var(--radius-sm)",
              minWidth: 220,
            }}
          />
          <select
            aria-label="Hidden filter"
            value={hidden}
            onChange={(e) => setFilter("hidden", e.target.value)}
            style={{
              padding: "0.4rem 0.6rem",
              border: "1px solid var(--c-border)",
              borderRadius: "var(--radius-sm)",
            }}
          >
            <option value="">All visibility</option>
            <option value="false">Public only</option>
            <option value="true">Hidden only</option>
          </select>
        </div>
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: selected
            ? "minmax(0, 1.1fr) minmax(0, 1fr)"
            : "1fr",
          gap: "1rem",
          alignItems: "start",
        }}
      >
        <Card padding={false}>
          {list.isLoading && <LoadingSkeleton type="table-row" rows={8} />}
          {list.error && (
            <div style={{ padding: "1rem" }}>
              <ErrorBlock
                message="Failed to load jobs"
                detail={String(list.error)}
              />
            </div>
          )}
          {list.data && (
            <>
              <table>
                <thead>
                  <tr>
                    <th>Title</th>
                    <th>Issuer</th>
                    <th>Country</th>
                    <th>Seen</th>
                    <th>State</th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.opportunities.map((j) => (
                    <tr
                      key={j.slug}
                      onClick={() => setSelected(j.slug)}
                      style={{
                        cursor: "pointer",
                        background:
                          selected === j.slug ? "#eef2ff" : undefined,
                      }}
                    >
                      <td>
                        <strong>{j.title || j.slug}</strong>
                        <div
                          style={{
                            fontSize: "0.75rem",
                            color: "var(--c-text-secondary)",
                          }}
                        >
                          <code>{j.kind}</code>
                        </div>
                      </td>
                      <td>{j.issuing_entity || "—"}</td>
                      <td>{j.country || "—"}</td>
                      <td style={{ whiteSpace: "nowrap" }}>
                        {new Date(j.last_seen_at).toLocaleString()}
                      </td>
                      <td>
                        {j.hidden ? (
                          <StatusBadge
                            variant="warning"
                            label="hidden"
                            size="sm"
                          />
                        ) : (
                          <StatusBadge
                            variant="success"
                            label="live"
                            size="sm"
                          />
                        )}
                      </td>
                    </tr>
                  ))}
                  {list.data.opportunities.length === 0 && (
                    <tr>
                      <td
                        colSpan={5}
                        style={{
                          padding: "1.5rem",
                          color: "var(--c-text-secondary)",
                        }}
                      >
                        No jobs match these filters.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
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
                  {list.data.total} total · page {page + 1}
                  {totalPages ? ` / ${totalPages}` : ""}
                </span>
                <div style={{ display: "flex", gap: "0.5rem" }}>
                  <Button
                    variant="outline"
                    disabled={page <= 0}
                    onClick={() => setFilter("page", String(page - 1))}
                  >
                    Prev
                  </Button>
                  <Button
                    variant="outline"
                    disabled={page + 1 >= totalPages}
                    onClick={() => setFilter("page", String(page + 1))}
                  >
                    Next
                  </Button>
                </div>
              </div>
            </>
          )}
        </Card>

        {selected && (
          <JobDetailPanel
            slug={selected}
            job={detail.data}
            loading={detail.isLoading}
            error={detail.error ? String(detail.error) : null}
            onClose={() => setSelected(null)}
            onHide={() => hideMut.mutate(selected)}
            onUnhide={() => unhideMut.mutate(selected)}
            onClearField={(field) =>
              clearFieldMut.mutate({
                slug: selected,
                clear_fields: [field],
              })
            }
            onClearAttr={(key) =>
              clearFieldMut.mutate({
                slug: selected,
                clear_attributes: [key],
              })
            }
            busy={
              hideMut.isPending ||
              unhideMut.isPending ||
              clearFieldMut.isPending
            }
          />
        )}
      </div>
    </div>
  );
}

function JobDetailPanel({
  slug,
  job,
  loading,
  error,
  onClose,
  onHide,
  onUnhide,
  onClearField,
  onClearAttr,
  busy,
}: {
  slug: string;
  job?: AdminOpportunity;
  loading: boolean;
  error: string | null;
  onClose: () => void;
  onHide: () => void;
  onUnhide: () => void;
  onClearField: (f: string) => void;
  onClearAttr: (k: string) => void;
  busy: boolean;
}) {
  const attrKeys = useMemo(
    () => (job?.attributes ? Object.keys(job.attributes).sort() : []),
    [job],
  );

  return (
    <Card>
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          gap: "0.5rem",
          marginBottom: "0.75rem",
        }}
      >
        <h2 style={{ margin: 0, fontSize: "1.05rem" }}>Sanitize</h2>
        <button
          type="button"
          onClick={onClose}
          style={{
            border: "none",
            background: "transparent",
            cursor: "pointer",
            fontSize: "1.1rem",
          }}
          aria-label="Close detail"
        >
          ×
        </button>
      </div>

      {loading && <LoadingSkeleton type="card" />}
      {error && <ErrorBlock message="Failed to load job" detail={error} />}
      {job && (
        <>
          <h3 style={{ margin: "0 0 0.25rem" }}>{job.title}</h3>
          <p
            style={{
              margin: "0 0 0.75rem",
              fontSize: "0.85rem",
              color: "var(--c-text-secondary)",
            }}
          >
            <Link to={`/opportunities/${encodeURIComponent(slug)}`}>
              {slug}
            </Link>
            {job.source_id ? (
              <>
                {" "}
                · source{" "}
                <Link to={`/sources/${encodeURIComponent(job.source_id)}`}>
                  {job.source_id}
                </Link>
              </>
            ) : null}
          </p>

          <div style={{ display: "flex", gap: "0.5rem", marginBottom: "1rem" }}>
            {job.hidden ? (
              <Button onClick={onUnhide} disabled={busy}>
                Unhide
              </Button>
            ) : (
              <Button
                variant="danger"
                onClick={() => {
                  if (
                    window.confirm(
                      "Hide this job from public search and detail pages?",
                    )
                  )
                    onHide();
                }}
                disabled={busy}
              >
                Hide job
              </Button>
            )}
            {job.apply_url && (
              <a href={job.apply_url} target="_blank" rel="noreferrer">
                <Button variant="outline">Open apply URL</Button>
              </a>
            )}
          </div>

          {job.hidden && job.hidden_reason && (
            <p style={{ fontSize: "0.85rem" }}>
              Hidden reason: <code>{job.hidden_reason}</code>
            </p>
          )}

          <h4 style={{ marginBottom: "0.35rem" }}>Clearable fields</h4>
          <ul style={{ paddingLeft: "1.1rem", marginTop: 0 }}>
            {(
              [
                ["description", job.description],
                ["issuing_entity", job.issuing_entity],
                ["region", job.region],
                ["city", job.city],
                ["currency", job.currency],
                ["employment_type", job.employment_type],
                ["seniority", job.seniority],
                ["geo_scope", job.geo_scope],
              ] as const
            ).map(([field, value]) =>
              value ? (
                <li key={field} style={{ marginBottom: "0.35rem" }}>
                  <code>{field}</code>:{" "}
                  <span style={{ color: "var(--c-text-secondary)" }}>
                    {String(value).slice(0, 80)}
                    {String(value).length > 80 ? "…" : ""}
                  </span>{" "}
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      if (window.confirm(`Clear ${field}?`)) onClearField(field);
                    }}
                    style={{
                      fontSize: "0.75rem",
                      cursor: "pointer",
                    }}
                  >
                    clear
                  </button>
                </li>
              ) : null,
            )}
          </ul>

          {attrKeys.length > 0 && (
            <>
              <h4 style={{ marginBottom: "0.35rem" }}>Attributes</h4>
              <ul style={{ paddingLeft: "1.1rem", marginTop: 0 }}>
                {attrKeys.map((k) => (
                  <li key={k} style={{ marginBottom: "0.35rem" }}>
                    <code>{k}</code>{" "}
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => {
                        if (window.confirm(`Remove attribute ${k}?`))
                          onClearAttr(k);
                      }}
                      style={{ fontSize: "0.75rem", cursor: "pointer" }}
                    >
                      remove
                    </button>
                  </li>
                ))}
              </ul>
            </>
          )}
        </>
      )}
    </Card>
  );
}
