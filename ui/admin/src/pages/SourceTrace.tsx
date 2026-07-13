import { useCallback, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  crawlSource,
  listCrawlRuns,
  pauseSource,
  resetCrawlRun,
  resumeSource,
  type CrawlRunRow,
  type SourceTraceResponse,
  fetchAdminJSON,
} from "@/api/admin-client";
import { Button, useToast } from "@/components/ui";

export function SourceTrace() {
  const { id } = useParams<{ id: string }>();
  const { toast } = useToast();
  const [data, setData] = useState<SourceTraceResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [runs, setRuns] = useState<CrawlRunRow[]>([]);
  const [busy, setBusy] = useState<string | null>(null);

  const reloadRuns = useCallback(() => {
    if (!id) return;
    listCrawlRuns(id, 20)
      .then((res) => setRuns(res.runs ?? []))
      .catch(() => setRuns([]));
  }, [id]);

  const reloadTrace = useCallback(() => {
    if (!id) return;
    fetchAdminJSON<SourceTraceResponse>(
      `/admin/trace/sources/${encodeURIComponent(id)}?since=24h`,
    )
      .then(setData)
      .catch((e: unknown) =>
        setErr(e instanceof Error ? e.message : String(e)),
      );
  }, [id]);

  useEffect(() => {
    if (!id) return;
    setData(null);
    setErr(null);
    reloadTrace();
    reloadRuns();
  }, [id, reloadTrace, reloadRuns]);

  const act = async (
    label: string,
    fn: () => Promise<unknown>,
    reload = true,
  ) => {
    if (!id) return;
    setBusy(label);
    try {
      await fn();
      toast(`${label} ok`, { type: "success" });
      if (reload) {
        reloadTrace();
        reloadRuns();
      }
    } catch (e: unknown) {
      toast(
        `${label} failed: ${e instanceof Error ? e.message : String(e)}`,
        { type: "error" },
      );
    } finally {
      setBusy(null);
    }
  };

  if (err) return <pre style={{ color: "crimson" }}>{err}</pre>;
  if (!data) return <p>Loading source trace…</p>;

  const { source, summary, recent_crawls } = data;
  const activeRun = runs.find(
    (r) => r.status === "running" || r.status === "paused",
  );

  return (
    <div>
      <header style={{ marginBottom: "1.25rem" }}>
        <h1 style={{ marginBottom: "0.35rem" }}>
          {source.id}{" "}
          <small style={{ color: "#666", fontWeight: "normal" }}>
            ({source.type})
          </small>
        </h1>
        <p style={{ margin: "0 0 0.75rem" }}>
          <strong>{source.base_url}</strong> · {source.country || "—"} · status{" "}
          <code>{source.status}</code> · health{" "}
          <code>{source.health_score.toFixed(2)}</code>
        </p>
        {source.next_crawl_at && (
          <p style={{ color: "#666", marginTop: 0 }}>
            Next crawl at {new Date(source.next_crawl_at).toLocaleString()}
          </p>
        )}

        <div style={{ display: "flex", flexWrap: "wrap", gap: "0.5rem" }}>
          <Button
            disabled={!!busy}
            onClick={() =>
              act("Crawl", () => crawlSource(id!), true)
            }
          >
            {busy === "Crawl" ? "Dispatching…" : "Crawl now"}
          </Button>
          <Button
            variant="outline"
            disabled={!!busy}
            onClick={() => act("Pause", () => pauseSource(id!))}
          >
            Pause
          </Button>
          <Button
            variant="outline"
            disabled={!!busy}
            onClick={() => act("Resume", () => resumeSource(id!))}
          >
            Resume
          </Button>
          <Link to={`/seeds/${encodeURIComponent(id!)}/digest`}>
            <Button variant="outline">Day digest</Button>
          </Link>
          <Link to={`/jobs?q=${encodeURIComponent(id!)}`}>
            <Button variant="outline">Jobs from source</Button>
          </Link>
        </div>
      </header>

      <section style={{ marginBottom: "1.5rem" }}>
        <h2>24-hour summary</h2>
        <table>
          <tbody>
            <tr>
              <td>Crawl jobs</td>
              <td>
                {summary.crawl_jobs} ({summary.crawl_jobs_failed} failed)
              </td>
            </tr>
            <tr>
              <td>Variants emitted</td>
              <td>{summary.variants_emitted}</td>
            </tr>
            <tr>
              <td>Variants published</td>
              <td>{summary.variants_published}</td>
            </tr>
            <tr>
              <td>Variants rejected</td>
              <td>{summary.variants_rejected}</td>
            </tr>
          </tbody>
        </table>
        {Object.keys(summary.rejection_reasons ?? {}).length > 0 && (
          <>
            <h3>Rejection reasons</h3>
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
      </section>

      <section style={{ marginBottom: "1.5rem" }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: "0.75rem",
          }}
        >
          <h2 style={{ margin: 0 }}>Crawl runs</h2>
          {activeRun && (
            <Button
              variant="outline"
              disabled={!!busy}
              onClick={() =>
                act("Reset run", () => resetCrawlRun(id!), true)
              }
            >
              {busy === "Reset run" ? "Resetting…" : "Reset active run"}
            </Button>
          )}
        </div>
        <p style={{ color: "#666", fontSize: "0.88rem" }}>
          Resumable slice state machine. Reset fails a wedged run so the next
          tick can start fresh.
        </p>
        {runs.length === 0 ? (
          <p style={{ color: "#666" }}>No crawl runs recorded for this source.</p>
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
              {runs.map((r) => (
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
                  <td style={{ maxWidth: 240 }}>
                    {r.error_code || r.error_message || ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section>
        <h2>Recent crawls</h2>
        {recent_crawls.length === 0 ? (
          <p style={{ color: "#666" }}>No crawls in the window.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Scheduled</th>
                <th>Status</th>
                <th>Found</th>
                <th>Stored</th>
                <th>Duration (ms)</th>
                <th>Error</th>
              </tr>
            </thead>
            <tbody>
              {recent_crawls.map((c) => (
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
                  <td>{c.error_code ?? ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
