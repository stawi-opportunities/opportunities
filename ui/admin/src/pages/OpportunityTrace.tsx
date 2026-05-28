import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import {
  getOpportunityTrace,
  type OpportunityTraceResponse,
} from '@/api/admin-client';

// OpportunityTrace renders GET /admin/trace/opportunities/{slug}: every
// pipeline variant that joined this canonical slug, with timestamps so
// operators can see when each upstream source last contributed.
export function OpportunityTrace() {
  const { slug } = useParams<{ slug: string }>();
  const [data, setData] = useState<OpportunityTraceResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!slug) return;
    setData(null);
    setErr(null);
    getOpportunityTrace(slug)
      .then(setData)
      .catch((e: unknown) =>
        setErr(e instanceof Error ? e.message : String(e))
      );
  }, [slug]);

  if (err) return <pre style={{ color: 'crimson' }}>{err}</pre>;
  if (!data) return <p>Loading opportunity…</p>;

  return (
    <div>
      <header>
        <h1>{data.slug}</h1>
        <p>{data.variant_count} variant(s) joined this canonical.</p>
      </header>

      <section>
        {data.variants.length === 0 ? (
          <p style={{ color: '#666' }}>
            No variants in the 7-day Postgres retention window.
          </p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Variant</th>
                <th>Source</th>
                <th>Ingested</th>
                <th>Joined</th>
              </tr>
            </thead>
            <tbody>
              {data.variants.map((v) => (
                <tr key={v.variant_id}>
                  <td>
                    <Link to={`/variants/${encodeURIComponent(v.variant_id)}`}>
                      {v.variant_id}
                    </Link>
                  </td>
                  <td>
                    <Link to={`/sources/${encodeURIComponent(v.source.id)}`}>
                      {v.source.id}
                    </Link>{' '}
                    ({v.source.type})
                  </td>
                  <td>{new Date(v.ingested_at).toLocaleString()}</td>
                  <td>{new Date(v.joined_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}
