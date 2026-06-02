import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  getVariantTrace,
  reparseRawPayload,
  type VariantTimelineResponse,
} from '@/api/admin-client';
import { TraceTimeline } from '@/components/TraceTimeline';

// VariantTrace renders GET /admin/trace/variants/{id}: full join across
// source, crawl_job, raw_payload + the stage transitions for one
// variant. Includes a "view captured HTML" deep-link and a one-click
// reparse button so operators can drive a re-extraction after a
// definition change.
export function VariantTrace() {
  const { id } = useParams<{ id: string }>();
  const [data, setData] = useState<VariantTimelineResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reparseMsg, setReparseMsg] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    setData(null);
    setErr(null);
    getVariantTrace(id)
      .then(setData)
      .catch((e: unknown) =>
        setErr(e instanceof Error ? e.message : String(e))
      );
  }, [id]);

  const handleReparse = async () => {
    if (!data?.raw_payload?.id) return;
    setReparseMsg('Queuing…');
    try {
      const res = await reparseRawPayload(data.raw_payload.id);
      setReparseMsg(`Queued ${res.queued} raw_payload(s) for re-extraction.`);
    } catch (e) {
      setReparseMsg(`Reparse failed: ${e instanceof Error ? e.message : String(e)}`);
    }
  };

  if (err) return <pre style={{ color: 'crimson' }}>{err}</pre>;
  if (!data) return <p>Loading variant…</p>;

  return (
    <div>
      <header>
        <h1>
          Variant <code>{data.variant_id}</code>
        </h1>
        <p>
          <strong>Source:</strong>{' '}
          <Link to={`/sources/${encodeURIComponent(data.source.id)}`}>
            {data.source.id}
          </Link>{' '}
          ({data.source.type})
          <br />
          <strong>Current stage:</strong> <code>{data.current_stage}</code>
          {data.opportunity_slug && (
            <>
              <br />
              <strong>Opportunity:</strong>{' '}
              <Link
                to={`/opportunities/${encodeURIComponent(data.opportunity_slug)}`}
              >
                {data.opportunity_slug}
              </Link>
            </>
          )}
        </p>
      </header>

      <section>
        <h2>Timeline</h2>
        <TraceTimeline stages={data.stages} />
      </section>

      {data.raw_payload && (
        <section>
          <h2>Raw payload</h2>
          <table>
            <tbody>
              <tr>
                <td>ID</td>
                <td>
                  <Link
                    to={`/raw_payloads/${encodeURIComponent(data.raw_payload.id)}`}
                  >
                    {data.raw_payload.id}
                  </Link>
                </td>
              </tr>
              {data.raw_payload.source_url && (
                <tr>
                  <td>Source URL</td>
                  <td>
                    <a
                      href={data.raw_payload.source_url}
                      target="_blank"
                      rel="noreferrer"
                    >
                      {data.raw_payload.source_url}
                    </a>
                  </td>
                </tr>
              )}
              {data.raw_payload.content_hash && (
                <tr>
                  <td>Content hash</td>
                  <td>
                    <code>{data.raw_payload.content_hash}</code>
                  </td>
                </tr>
              )}
              <tr>
                <td>Size</td>
                <td>{data.raw_payload.size_bytes} B</td>
              </tr>
              <tr>
                <td>HTTP status</td>
                <td>{data.raw_payload.http_status}</td>
              </tr>
              <tr>
                <td>Fetched at</td>
                <td>{new Date(data.raw_payload.fetched_at).toLocaleString()}</td>
              </tr>
            </tbody>
          </table>
          <p style={{ marginTop: '0.5rem' }}>
            {data.raw_payload.body_url && (
              <a
                href={data.raw_payload.body_url}
                target="_blank"
                rel="noreferrer"
              >
                View captured HTML →
              </a>
            )}
            <button
              type="button"
              onClick={handleReparse}
              style={{ marginLeft: '1rem' }}
            >
              Reparse
            </button>
            {reparseMsg && (
              <small style={{ marginLeft: '0.5rem' }}>{reparseMsg}</small>
            )}
          </p>
        </section>
      )}

      {data.last_error && (
        <section>
          <h2>Last error</h2>
          <pre
            style={{
              color: 'crimson',
              background: '#fee',
              padding: '0.5rem',
              whiteSpace: 'pre-wrap',
            }}
          >
            {data.last_error}
          </pre>
        </section>
      )}
    </div>
  );
}
