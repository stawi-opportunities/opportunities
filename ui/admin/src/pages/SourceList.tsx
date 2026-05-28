import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { listSources, type SourceListItem } from '@/api/admin-client';

// SourceList is the landing page: a table of recent sources with health
// + status, each row linking to the per-source trace and the per-day
// seed digest.
export function SourceList() {
  const [data, setData] = useState<SourceListItem[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    listSources(100)
      .then(setData)
      .catch((e: unknown) =>
        setErr(e instanceof Error ? e.message : String(e))
      );
  }, []);

  if (err) return <pre style={{ color: 'crimson' }}>{err}</pre>;
  if (!data) return <p>Loading sources…</p>;

  if (data.length === 0) {
    return (
      <div>
        <h1>Sources</h1>
        <p style={{ color: '#666' }}>No sources configured.</p>
      </div>
    );
  }

  return (
    <div>
      <h1>Sources</h1>
      <table>
        <thead>
          <tr>
            <th>ID</th>
            <th>Type</th>
            <th>Status</th>
            <th>Country</th>
            <th>Health</th>
            <th>Digest</th>
          </tr>
        </thead>
        <tbody>
          {data.map((s) => (
            <tr key={s.id}>
              <td>
                <Link to={`/sources/${encodeURIComponent(s.id)}`}>{s.id}</Link>
              </td>
              <td>{s.type}</td>
              <td>{s.status}</td>
              <td>{s.country}</td>
              <td>
                {typeof s.health_score === 'number'
                  ? s.health_score.toFixed(2)
                  : '—'}
              </td>
              <td>
                <Link to={`/seeds/${encodeURIComponent(s.id)}/digest`}>
                  digest →
                </Link>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
