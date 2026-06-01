import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  listDefinitions,
  type DefinitionEntry,
  type DefinitionsListResponse,
} from '@/api/admin-client';

// DefinitionsList renders GET /admin/definitions: a per-type grouping of
// every definition (kinds, prompts, connectors, seeds, etc.) stored in
// R2, with size + version + last-updated. Each row deep-links to the
// editor for that type+name.
export function DefinitionsList() {
  const [data, setData] = useState<DefinitionsListResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    listDefinitions()
      .then(setData)
      .catch((e: unknown) =>
        setErr(e instanceof Error ? e.message : String(e))
      );
  }, []);

  if (err) return <pre style={{ color: 'crimson' }}>{err}</pre>;
  if (!data) return <p>Loading definitions…</p>;

  const types = Object.keys(data).sort();
  if (types.length === 0) {
    return (
      <div>
        <h1>Definitions</h1>
        <p style={{ color: '#666' }}>No definitions stored.</p>
      </div>
    );
  }

  return (
    <div>
      <h1>Definitions</h1>
      {types.map((type) => {
        const entries = data[type] ?? [];
        return (
          <section key={type}>
            <h2>{type}</h2>
            {entries.length === 0 ? (
              <p style={{ color: '#666' }}>(none)</p>
            ) : (
              <table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Version</th>
                    <th>Updated</th>
                    <th>Size</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {entries.map((e: DefinitionEntry) => (
                    <tr key={`${type}/${e.name}`}>
                      <td>{e.name}</td>
                      <td>
                        <code>{e.version.slice(0, 12)}</code>
                      </td>
                      <td>{new Date(e.updated_at).toLocaleString()}</td>
                      <td>{e.size} B</td>
                      <td>
                        <Link
                          to={`/definitions/${encodeURIComponent(type)}/${encodeURIComponent(e.name)}`}
                        >
                          edit
                        </Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        );
      })}
    </div>
  );
}
