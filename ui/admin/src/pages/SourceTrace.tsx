import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import {
  clearCheckpoint,
  fetchAdminJSON,
  listCheckpoints,
  type CheckpointRow,
  type SourceTraceResponse,
} from '@/api/admin-client';

// Renders GET /admin/trace/sources/{id}?since=24h as a 24-hour summary
// plus a recent-crawls table. The endpoint is implemented in
// apps/api/cmd/trace_admin.go and tested in trace_admin_test.go.
export function SourceTrace() {
  const { id } = useParams<{ id: string }>();
  const [data, setData] = useState<SourceTraceResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [checkpoints, setCheckpoints] = useState<CheckpointRow[]>([]);
  const [cpBusy, setCpBusy] = useState<string | null>(null);

  const reloadCheckpoints = useCallback(() => {
    if (!id) return;
    listCheckpoints(id)
      .then((res) => setCheckpoints(res.checkpoints))
      .catch(() => setCheckpoints([])); // soft-fail: trace UI keeps rendering
  }, [id]);

  useEffect(() => {
    if (!id) return;
    setData(null);
    setErr(null);
    fetchAdminJSON<SourceTraceResponse>(
      `/admin/trace/sources/${encodeURIComponent(id)}?since=24h`
    )
      .then(setData)
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
    reloadCheckpoints();
  }, [id, reloadCheckpoints]);

  const onReset = async (connectorType: string) => {
    if (!id) return;
    if (!window.confirm(`Reset checkpoint for ${id}/${connectorType}? Next crawl will start from page 1.`)) {
      return;
    }
    setCpBusy(connectorType);
    try {
      await clearCheckpoint(id, connectorType);
      reloadCheckpoints();
    } catch (e: unknown) {
      window.alert('Failed to clear checkpoint: ' + (e instanceof Error ? e.message : String(e)));
    } finally {
      setCpBusy(null);
    }
  };

  if (err) return <pre style={{ color: 'crimson' }}>{err}</pre>;
  if (!data) return <p>Loading source trace…</p>;

  const { source, summary, recent_crawls } = data;

  return (
    <div>
      <header>
        <h1>
          {source.id}{' '}
          <small style={{ color: '#666', fontWeight: 'normal' }}>({source.type})</small>
        </h1>
        <p>
          <strong>{source.base_url}</strong> · {source.country} · status{' '}
          <code>{source.status}</code> · health{' '}
          <code>{source.health_score.toFixed(2)}</code>
        </p>
        {source.next_crawl_at && (
          <p style={{ color: '#666' }}>
            Next crawl at {new Date(source.next_crawl_at).toLocaleString()}
          </p>
        )}
      </header>

      <section>
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
        {Object.keys(summary.rejection_reasons).length > 0 && (
          <>
            <h3>Rejection reasons</h3>
            <ul>
              {Object.entries(summary.rejection_reasons).map(([reason, count]) => (
                <li key={reason}>
                  <code>{reason}</code> × {count}
                </li>
              ))}
            </ul>
          </>
        )}
      </section>

      <section>
        <h2>Iterator checkpoints</h2>
        {checkpoints.length === 0 ? (
          <p style={{ color: '#666' }}>
            No active checkpoints — last crawl completed cleanly (or
            the connector doesn't participate in resume).
          </p>
        ) : (
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
                  <td><code>{c.connector_type}</code></td>
                  <td>{c.page_idx}</td>
                  <td style={{ maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {c.last_url ?? ''}
                  </td>
                  <td>{new Date(c.last_checkpoint_at).toLocaleString()}</td>
                  <td>
                    <button
                      onClick={() => onReset(c.connector_type)}
                      disabled={cpBusy === c.connector_type}
                    >
                      {cpBusy === c.connector_type ? 'Clearing…' : 'Reset checkpoint'}
                    </button>
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
          <p style={{ color: '#666' }}>No crawls in the window.</p>
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
                  <td>{new Date(c.scheduled_at).toLocaleString()}</td>
                  <td>{c.status}</td>
                  <td>{c.jobs_found}</td>
                  <td>{c.jobs_stored}</td>
                  <td>{c.duration_ms}</td>
                  <td>{c.error_code ?? ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
