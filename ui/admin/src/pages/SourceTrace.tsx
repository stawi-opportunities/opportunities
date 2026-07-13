import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  approveSource,
  crawlSource,
  deleteSource,
  generateRecipe,
  getActiveRecipe,
  getSource,
  getSourceTrace,
  listCrawlRuns,
  listRecipeHistory,
  pauseSource,
  putRecipe,
  rejectSource,
  resetCrawlRun,
  resumeSource,
  rollbackRecipe,
  startSource,
  stopSource,
  testRecipe,
  testSourceExtract,
  updateSource,
  verifySource,
  type CrawlRunRow,
  type RecipeTestReport,
  type SourceTraceResponse,
  type UpdateSourceRequest,
} from "@/api/admin-client";
import { Button, Card, ErrorBlock, LoadingSkeleton, StatusBadge, useToast } from "@/components/ui";

const fieldStyle: React.CSSProperties = {
  width: "100%",
  padding: "0.4rem 0.55rem",
  border: "1px solid var(--c-border)",
  borderRadius: "var(--radius-sm)",
  fontSize: "0.85rem",
  boxSizing: "border-box",
};

type Tab = "overview" | "settings" | "recipe" | "runs";

export function SourceTrace() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { toast } = useToast();
  const qc = useQueryClient();
  const [tab, setTab] = useState<Tab>("overview");
  const [busy, setBusy] = useState<string | null>(null);
  const [recipeJSON, setRecipeJSON] = useState("");
  const [testReport, setTestReport] = useState<RecipeTestReport | null>(null);

  const source = useQuery({
    queryKey: ["source", id],
    queryFn: () => getSource(id!),
    enabled: !!id,
  });

  const trace = useQuery({
    queryKey: ["source-trace", id],
    queryFn: () => getSourceTrace(id!, "24h"),
    enabled: !!id,
    refetchInterval: 30_000,
  });

  const runs = useQuery({
    queryKey: ["crawl-runs", id],
    queryFn: () => listCrawlRuns(id!, 20),
    enabled: !!id,
  });

  const recipe = useQuery({
    queryKey: ["recipe", id],
    queryFn: () => getActiveRecipe(id!),
    enabled: !!id,
  });

  const history = useQuery({
    queryKey: ["recipe-history", id],
    queryFn: () => listRecipeHistory(id!),
    enabled: !!id && tab === "recipe",
  });

  useEffect(() => {
    if (recipe.data?.recipe) {
      setRecipeJSON(JSON.stringify(recipe.data.recipe, null, 2));
    } else if (recipe.data && !recipe.data.has_recipe) {
      setRecipeJSON("");
    }
  }, [recipe.data]);

  const invalidate = useCallback(() => {
    qc.invalidateQueries({ queryKey: ["source", id] });
    qc.invalidateQueries({ queryKey: ["source-trace", id] });
    qc.invalidateQueries({ queryKey: ["crawl-runs", id] });
    qc.invalidateQueries({ queryKey: ["recipe", id] });
    qc.invalidateQueries({ queryKey: ["recipe-history", id] });
    qc.invalidateQueries({ queryKey: ["sources"] });
  }, [qc, id]);

  const act = async (label: string, fn: () => Promise<unknown>) => {
    if (!id) return;
    setBusy(label);
    try {
      await fn();
      toast(`${label} ok`, { type: "success" });
      invalidate();
    } catch (e: unknown) {
      toast(
        `${label} failed: ${e instanceof Error ? e.message : String(e)}`,
        { type: "error" },
      );
    } finally {
      setBusy(null);
    }
  };

  const saveSettings = useMutation({
    mutationFn: (body: UpdateSourceRequest) => updateSource(id!, body),
    onSuccess: () => {
      toast("Settings saved", { type: "success" });
      invalidate();
    },
    onError: (e: Error) => toast(e.message, { type: "error" }),
  });

  if (!id) return <ErrorBlock message="Missing source id" />;
  if (source.isLoading) return <LoadingSkeleton type="card" />;
  if (source.error)
    return (
      <ErrorBlock message="Failed to load source" detail={String(source.error)} />
    );
  if (!source.data) return null;

  const s = source.data;
  const summary = (trace.data as SourceTraceResponse | undefined)?.summary;
  const recent = (trace.data as SourceTraceResponse | undefined)?.recent_crawls ?? [];
  const runRows: CrawlRunRow[] = runs.data?.runs ?? [];
  const activeRun = runRows.find(
    (r) => r.status === "running" || r.status === "paused",
  );

  return (
    <div>
      <header style={{ marginBottom: "1rem" }}>
        <Link to="/sources" style={{ fontSize: "0.85rem" }}>
          ← Sources
        </Link>
        <h1 style={{ margin: "0.35rem 0 0.25rem" }}>
          {s.name || s.id}{" "}
          <small style={{ color: "#666", fontWeight: "normal" }}>
            ({s.type})
          </small>
        </h1>
        <p style={{ margin: "0 0 0.5rem" }}>
          <strong>{s.base_url}</strong>
          {s.listing_path ? (
            <span style={{ color: "#666" }}> + {s.listing_path}</span>
          ) : null}{" "}
          · {s.country || "—"} ·{" "}
          <StatusBadge
            variant={
              s.status === "active"
                ? "success"
                : s.status === "paused" || s.status === "pending"
                  ? "warning"
                  : "neutral"
            }
            label={s.status}
            size="sm"
          />{" "}
          · health {(s.health_score ?? 0).toFixed(2)}
          {s.needs_tuning ? (
            <>
              {" "}
              <StatusBadge variant="warning" label="needs tuning" size="sm" />
            </>
          ) : null}
          {recipe.data?.has_recipe ? (
            <>
              {" "}
              <StatusBadge variant="success" label="has recipe" size="sm" />
            </>
          ) : (
            <>
              {" "}
              <StatusBadge variant="neutral" label="no recipe" size="sm" />
            </>
          )}
        </p>

        <div style={{ display: "flex", flexWrap: "wrap", gap: "0.4rem" }}>
          <Button
            disabled={!!busy}
            onClick={() => act("Crawl", () => crawlSource(id))}
          >
            {busy === "Crawl" ? "…" : "Crawl now"}
          </Button>
          <Button
            variant="outline"
            disabled={!!busy}
            onClick={() =>
              act("Test extract", async () => {
                const r = await testSourceExtract(id);
                setTestReport(r);
                setTab("recipe");
              })
            }
          >
            Test extract
          </Button>
          <Button
            variant="outline"
            disabled={!!busy}
            onClick={() => act("Verify", () => verifySource(id))}
          >
            Verify
          </Button>
          {s.status === "verified" && (
            <Button
              disabled={!!busy}
              onClick={() => act("Approve", () => approveSource(id))}
            >
              Approve
            </Button>
          )}
          {(s.status === "pending" ||
            s.status === "verified" ||
            s.status === "verifying") && (
            <Button
              variant="outline"
              disabled={!!busy}
              onClick={() => {
                const reason = window.prompt("Rejection reason?", "not suitable");
                if (reason != null)
                  act("Reject", () => rejectSource(id, reason));
              }}
            >
              Reject
            </Button>
          )}
          {s.status === "active" || s.status === "degraded" ? (
            <Button
              variant="outline"
              disabled={!!busy}
              onClick={() => act("Pause", () => pauseSource(id))}
            >
              Pause
            </Button>
          ) : null}
          {s.status === "paused" ? (
            <Button
              variant="outline"
              disabled={!!busy}
              onClick={() => act("Resume", () => resumeSource(id))}
            >
              Resume
            </Button>
          ) : null}
          {s.status !== "disabled" ? (
            <Button
              variant="outline"
              disabled={!!busy}
              onClick={() => {
                if (window.confirm("Stop (disable) this source?"))
                  act("Stop", () => stopSource(id));
              }}
            >
              Stop
            </Button>
          ) : (
            <Button
              variant="outline"
              disabled={!!busy}
              onClick={() => act("Start", () => startSource(id))}
            >
              Start
            </Button>
          )}
          <Button
            variant="danger"
            disabled={!!busy}
            onClick={() => {
              if (
                window.confirm(
                  "Disable this source? (soft delete — use hard only if sure)",
                )
              )
                act("Delete", async () => {
                  await deleteSource(id, false);
                  navigate("/sources");
                });
            }}
          >
            Disable
          </Button>
          <Link to={`/seeds/${encodeURIComponent(id)}/digest`}>
            <Button variant="outline">Day digest</Button>
          </Link>
        </div>
      </header>

      <div
        style={{
          display: "flex",
          gap: "0.4rem",
          marginBottom: "1rem",
          flexWrap: "wrap",
        }}
      >
        {(
          [
            ["overview", "Overview"],
            ["settings", "Settings"],
            ["recipe", "Recipe & test"],
            ["runs", "Crawl runs"],
          ] as const
        ).map(([k, label]) => (
          <Button
            key={k}
            size="sm"
            variant={tab === k ? "primary" : "outline"}
            onClick={() => setTab(k)}
          >
            {label}
          </Button>
        ))}
      </div>

      {tab === "overview" && (
        <div
          style={{
            display: "grid",
            gap: "1rem",
            gridTemplateColumns: "minmax(0,1fr) minmax(0,1.2fr)",
          }}
        >
          <Card>
            <h2 style={{ marginTop: 0, fontSize: "1rem" }}>24h summary</h2>
            {trace.isLoading && <p>Loading…</p>}
            {summary && (
              <table>
                <tbody>
                  <tr>
                    <td>Crawl jobs</td>
                    <td>
                      {summary.crawl_jobs} ({summary.crawl_jobs_failed} failed)
                    </td>
                  </tr>
                  <tr>
                    <td>Emitted</td>
                    <td>{summary.variants_emitted}</td>
                  </tr>
                  <tr>
                    <td>Published</td>
                    <td>{summary.variants_published}</td>
                  </tr>
                  <tr>
                    <td>Rejected</td>
                    <td>{summary.variants_rejected}</td>
                  </tr>
                </tbody>
              </table>
            )}
            {summary &&
              Object.keys(summary.rejection_reasons ?? {}).length > 0 && (
                <>
                  <h3 style={{ fontSize: "0.9rem" }}>Rejection reasons</h3>
                  <ul>
                    {Object.entries(summary.rejection_reasons).map(
                      ([reason, count]) => (
                        <li key={reason}>
                          <code>{reason}</code> × {count}
                        </li>
                      ),
                    )}
                  </ul>
                </>
              )}
            {s.verification_report && (
              <>
                <h3 style={{ fontSize: "0.9rem" }}>Last verification</h3>
                <pre
                  style={{
                    fontSize: "0.75rem",
                    overflow: "auto",
                    maxHeight: 200,
                    background: "#f8fafc",
                    padding: "0.5rem",
                    borderRadius: 4,
                  }}
                >
                  {JSON.stringify(s.verification_report, null, 2)}
                </pre>
              </>
            )}
          </Card>
          <Card padding={false}>
            <div style={{ padding: "0.75rem 1rem 0" }}>
              <h2 style={{ margin: 0, fontSize: "1rem" }}>Recent crawls</h2>
            </div>
            {recent.length === 0 ? (
              <p
                style={{
                  color: "var(--c-text-secondary)",
                  padding: "0.75rem 1rem",
                }}
              >
                No crawls in the window.
              </p>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>Scheduled</th>
                    <th>Status</th>
                    <th>Found</th>
                    <th>Stored</th>
                    <th>ms</th>
                  </tr>
                </thead>
                <tbody>
                  {recent.map((c) => (
                    <tr key={c.crawl_job_id}>
                      <td style={{ whiteSpace: "nowrap" }}>
                        {new Date(c.scheduled_at).toLocaleString()}
                      </td>
                      <td>
                        <code>{c.status}</code>
                      </td>
                      <td>{c.jobs_found}</td>
                      <td>{c.jobs_stored}</td>
                      <td>{c.duration_ms}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Card>
        </div>
      )}

      {tab === "settings" && (
        <Card>
          <h2 style={{ marginTop: 0, fontSize: "1rem" }}>Edit source</h2>
          <SettingsForm
            source={s}
            busy={saveSettings.isPending}
            onSave={(body) => saveSettings.mutate(body)}
          />
        </Card>
      )}

      {tab === "recipe" && (
        <div style={{ display: "grid", gap: "1rem" }}>
          <Card>
            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                gap: "0.5rem",
                flexWrap: "wrap",
                alignItems: "center",
              }}
            >
              <h2 style={{ margin: 0, fontSize: "1rem" }}>
                Extraction recipe
              </h2>
              <div style={{ display: "flex", gap: "0.4rem", flexWrap: "wrap" }}>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!!busy}
                  onClick={() =>
                    act("Generate recipe", () => generateRecipe(id))
                  }
                >
                  Queue AI generate
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!!busy || !recipeJSON.trim()}
                  onClick={() =>
                    act("Test recipe", async () => {
                      let parsed: unknown;
                      try {
                        parsed = recipeJSON.trim()
                          ? JSON.parse(recipeJSON)
                          : undefined;
                      } catch {
                        throw new Error("Recipe JSON is invalid");
                      }
                      const r = await testRecipe(id, parsed, 3);
                      setTestReport(r);
                    })
                  }
                >
                  Test recipe
                </Button>
                <Button
                  size="sm"
                  disabled={!!busy || !recipeJSON.trim()}
                  onClick={() =>
                    act("Activate recipe", async () => {
                      const parsed = JSON.parse(recipeJSON);
                      await putRecipe(id, parsed, true);
                      invalidate();
                    })
                  }
                >
                  Save & activate
                </Button>
              </div>
            </div>
            <p
              style={{
                fontSize: "0.82rem",
                color: "var(--c-text-secondary)",
                margin: "0.5rem 0",
              }}
            >
              Install a deterministic recipe (JSON). Use{" "}
              <strong>Test recipe</strong> to dry-run against the live site
              without storing jobs. <strong>Queue AI generate</strong> emits{" "}
              <code>recipe.generate.v1</code> for the crawler (needs inference
              + events). For JSON API connectors, a recipe is optional — use{" "}
              <strong>Test extract</strong> above.
            </p>
            <textarea
              value={recipeJSON}
              onChange={(e) => setRecipeJSON(e.target.value)}
              placeholder='{"acquisition":"api", ...}'
              spellCheck={false}
              style={{
                width: "100%",
                minHeight: 280,
                fontFamily: "ui-monospace, monospace",
                fontSize: "0.78rem",
                padding: "0.6rem",
                border: "1px solid var(--c-border)",
                borderRadius: "var(--radius-sm)",
                boxSizing: "border-box",
              }}
            />
          </Card>

          {testReport && (
            <Card>
              <h2 style={{ marginTop: 0, fontSize: "1rem" }}>
                Test report{" "}
                {testReport.verified ? (
                  <StatusBadge variant="success" label="verified" size="sm" />
                ) : (
                  <StatusBadge variant="warning" label="not verified" size="sm" />
                )}
              </h2>
              <pre
                style={{
                  fontSize: "0.75rem",
                  overflow: "auto",
                  maxHeight: 420,
                  background: "#f8fafc",
                  padding: "0.75rem",
                  borderRadius: 4,
                }}
              >
                {JSON.stringify(testReport, null, 2)}
              </pre>
            </Card>
          )}

          {history.data && history.data.versions.length > 0 && (
            <Card padding={false}>
              <div style={{ padding: "0.75rem 1rem 0" }}>
                <h2 style={{ margin: 0, fontSize: "1rem" }}>Version history</h2>
              </div>
              <table>
                <thead>
                  <tr>
                    <th>Ver</th>
                    <th>Status</th>
                    <th>Pass rate</th>
                    <th>Model</th>
                    <th>Created</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {history.data.versions.map((v) => (
                    <tr key={v.id}>
                      <td>v{v.version}</td>
                      <td>
                        <code>{v.status}</code>
                      </td>
                      <td>{(v.pass_rate * 100).toFixed(0)}%</td>
                      <td>{v.model || "—"}</td>
                      <td style={{ whiteSpace: "nowrap" }}>
                        {new Date(v.created_at).toLocaleString()}
                      </td>
                      <td>
                        {v.status !== "active" && (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={!!busy}
                            onClick={() =>
                              act(`Rollback v${v.version}`, () =>
                                rollbackRecipe(id, v.version),
                              )
                            }
                          >
                            Rollback
                          </Button>
                        )}
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() =>
                            setRecipeJSON(
                              JSON.stringify(v.recipe, null, 2),
                            )
                          }
                        >
                          Load
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

      {tab === "runs" && (
        <Card padding={false}>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              padding: "0.75rem 1rem",
              alignItems: "center",
            }}
          >
            <h2 style={{ margin: 0, fontSize: "1rem" }}>Crawl runs</h2>
            {activeRun && (
              <Button
                size="sm"
                variant="outline"
                disabled={!!busy}
                onClick={() => act("Reset run", () => resetCrawlRun(id))}
              >
                Reset active run
              </Button>
            )}
          </div>
          {runRows.length === 0 ? (
            <p
              style={{
                color: "var(--c-text-secondary)",
                padding: "0 1rem 1rem",
              }}
            >
              No crawl runs yet.
            </p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Status</th>
                  <th>Started</th>
                  <th>Found</th>
                  <th>Stored</th>
                  <th>Rejected</th>
                  <th>Error</th>
                </tr>
              </thead>
              <tbody>
                {runRows.map((r) => (
                  <tr key={r.id}>
                    <td>
                      <code>{r.id.slice(0, 10)}…</code>
                    </td>
                    <td>
                      <code>{r.status}</code>
                    </td>
                    <td style={{ whiteSpace: "nowrap" }}>
                      {r.started_at
                        ? new Date(r.started_at).toLocaleString()
                        : "—"}
                    </td>
                    <td>{r.jobs_found ?? 0}</td>
                    <td>{r.jobs_stored ?? 0}</td>
                    <td>{r.jobs_rejected ?? 0}</td>
                    <td style={{ maxWidth: 220 }}>
                      {r.error_code || r.error_message || ""}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>
      )}
    </div>
  );
}

function SettingsForm({
  source,
  busy,
  onSave,
}: {
  source: {
    name?: string;
    country?: string;
    language?: string;
    crawl_interval_sec?: number;
    listing_path?: string;
    kinds?: string[];
    auto_approve?: boolean;
    frontier_enabled?: boolean;
    extraction_prompt_extension?: string;
  };
  busy: boolean;
  onSave: (body: UpdateSourceRequest) => void;
}) {
  const [name, setName] = useState(source.name ?? "");
  const [country, setCountry] = useState(source.country ?? "");
  const [language, setLanguage] = useState(source.language ?? "en");
  const [interval, setInterval] = useState(source.crawl_interval_sec ?? 3600);
  const [listingPath, setListingPath] = useState(source.listing_path ?? "");
  const [kinds, setKinds] = useState((source.kinds ?? ["job"]).join(", "));
  const [autoApprove, setAutoApprove] = useState(!!source.auto_approve);
  const [frontier, setFrontier] = useState(!!source.frontier_enabled);
  const [promptExt, setPromptExt] = useState(
    source.extraction_prompt_extension ?? "",
  );

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSave({
          name: name.trim() || undefined,
          country: country.trim().toUpperCase() || undefined,
          language: language.trim() || undefined,
          crawl_interval_sec: interval,
          listing_path: listingPath.trim(),
          kinds: kinds
            .split(",")
            .map((k) => k.trim())
            .filter(Boolean),
          auto_approve: autoApprove,
          frontier_enabled: frontier,
          extraction_prompt_extension: promptExt,
        });
      }}
    >
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          gap: "0.75rem",
        }}
      >
        <div>
          <label style={{ fontSize: "0.8rem", fontWeight: 600 }}>Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            style={fieldStyle}
          />
        </div>
        <div>
          <label style={{ fontSize: "0.8rem", fontWeight: 600 }}>
            Country
          </label>
          <input
            value={country}
            onChange={(e) => setCountry(e.target.value)}
            style={fieldStyle}
          />
        </div>
        <div>
          <label style={{ fontSize: "0.8rem", fontWeight: 600 }}>
            Language
          </label>
          <input
            value={language}
            onChange={(e) => setLanguage(e.target.value)}
            style={fieldStyle}
          />
        </div>
        <div>
          <label style={{ fontSize: "0.8rem", fontWeight: 600 }}>
            Crawl interval (sec)
          </label>
          <input
            type="number"
            min={60}
            value={interval}
            onChange={(e) => setInterval(Number(e.target.value) || 3600)}
            style={fieldStyle}
          />
        </div>
        <div style={{ gridColumn: "1 / -1" }}>
          <label style={{ fontSize: "0.8rem", fontWeight: 600 }}>
            Listing path
          </label>
          <input
            value={listingPath}
            onChange={(e) => setListingPath(e.target.value)}
            placeholder="/jobs"
            style={fieldStyle}
          />
        </div>
        <div style={{ gridColumn: "1 / -1" }}>
          <label style={{ fontSize: "0.8rem", fontWeight: 600 }}>Kinds</label>
          <input
            value={kinds}
            onChange={(e) => setKinds(e.target.value)}
            style={fieldStyle}
          />
        </div>
        <div style={{ gridColumn: "1 / -1" }}>
          <label style={{ fontSize: "0.8rem", fontWeight: 600 }}>
            Extraction prompt extension
          </label>
          <textarea
            value={promptExt}
            onChange={(e) => setPromptExt(e.target.value)}
            rows={3}
            style={{ ...fieldStyle, fontFamily: "inherit" }}
          />
        </div>
      </div>
      <div style={{ margin: "0.75rem 0", display: "flex", gap: "1rem" }}>
        <label style={{ fontSize: "0.85rem" }}>
          <input
            type="checkbox"
            checked={autoApprove}
            onChange={(e) => setAutoApprove(e.target.checked)}
          />{" "}
          Auto-approve
        </label>
        <label style={{ fontSize: "0.85rem" }}>
          <input
            type="checkbox"
            checked={frontier}
            onChange={(e) => setFrontier(e.target.checked)}
          />{" "}
          Frontier enabled
        </label>
      </div>
      <Button type="submit" disabled={busy}>
        {busy ? "Saving…" : "Save settings"}
      </Button>
    </form>
  );
}
