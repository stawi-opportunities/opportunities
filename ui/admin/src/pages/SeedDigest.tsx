import { useEffect, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import {
  getSeedDigest,
  reparseSource,
  type SeedDigestResponse,
} from '@/api/admin-client';
import { RejectionChart } from '@/components/RejectionChart';

// SeedDigest renders GET /admin/trace/seeds/{id}/digest?date=YYYY-MM-DD:
// a one-day rollup of crawl jobs, variants emitted/published/rejected
// plus the reason histogram. Recent dates (≤7d) come from Postgres,
// older dates fall back to Iceberg — the `data_source` field exposes
// which one supplied the numbers.
export function SeedDigest() {
  const { id } = useParams<{ id: string }>();
  const [params, setParams] = useSearchParams();
  const date = params.get('date') ?? new Date().toISOString().slice(0, 10);
  const [data, setData] = useState<SeedDigestResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reparseMsg, setReparseMsg] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    setData(null);
    setErr(null);
    getSeedDigest(id, date)
      .then(setData)
      .catch((e: unknown) =>
        setErr(e instanceof Error ? e.message : String(e))
      );
  }, [id, date]);

  const handleReparse = async () => {
    if (!id) return;
    setReparseMsg('Queuing…');
    try {
      const res = await reparseSource(id, '24h');
      setReparseMsg(
        `Queued ${res.queued} raw_payload(s) for re-extraction (window: ${
          res.window_seconds ?? '?'
        }s).`
      );
    } catch (e) {
      setReparseMsg(
        `Reparse failed: ${e instanceof Error ? e.message : String(e)}`
      );
    }
  };

  return (
    <div>
      <header>
        <h1>
          Digest{' '}
          <small style={{ fontWeight: 'normal', color: '#666' }}>
            for{' '}
            <Link to={`/sources/${encodeURIComponent(id ?? '')}`}>{id}</Link> on{' '}
            {date}
          </small>
        </h1>
        <p style={{ display: 'flex', gap: '0.75rem', alignItems: 'center' }}>
          <label>
            Date:{' '}
            <input
              type="date"
              value={date}
              onChange={(e) => setParams({ date: e.target.value })}
            />
          </label>
          <button type="button" onClick={handleReparse}>
            Reparse last 24h
          </button>
          {reparseMsg && <small>{reparseMsg}</small>}
        </p>
      </header>

      {err && <pre style={{ color: 'crimson' }}>{err}</pre>}
      {data && (
        <>
          <section>
            <h2>Counts</h2>
            <table>
              <tbody>
                {data.crawl_jobs != null && (
                  <tr>
                    <td>Crawl jobs</td>
                    <td>{data.crawl_jobs}</td>
                  </tr>
                )}
                <tr>
                  <td>Variants emitted</td>
                  <td>{data.variants_emitted}</td>
                </tr>
                <tr>
                  <td>Variants published</td>
                  <td>{data.variants_published}</td>
                </tr>
                <tr>
                  <td>Variants rejected</td>
                  <td>{data.variants_rejected}</td>
                </tr>
              </tbody>
            </table>
            <p style={{ marginTop: '0.5rem' }}>
              <small>
                data source: <code>{data.data_source}</code>
              </small>
            </p>
          </section>

          <section>
            <h2>Rejection reasons</h2>
            <RejectionChart reasons={data.rejection_reasons ?? {}} />
          </section>
        </>
      )}
    </div>
  );
}
