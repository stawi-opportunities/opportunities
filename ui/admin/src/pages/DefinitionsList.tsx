import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { listDefinitions, type DefinitionEntry } from '@/api/admin-client';
import { Card, ErrorBlock, LoadingSkeleton } from '@/components/ui';

export function DefinitionsList() {
  const [activeType, setActiveType] = useState<string | null>(null);
  const [search, setSearch] = useState('');

  const { data, isLoading, error } = useQuery({
    queryKey: ['definitions'],
    queryFn: () => listDefinitions(),
    refetchInterval: 60_000,
  });

  const types = useMemo(() => (data ? Object.keys(data).sort() : []), [data]);

  const currentType = activeType ?? types[0] ?? '';

  const entries = useMemo(() => {
    if (!data) return [];
    const all = data[currentType] ?? [];
    if (!search.trim()) return all;
    const q = search.toLowerCase();
    return all.filter((e: DefinitionEntry) => e.name.toLowerCase().includes(q));
  }, [data, currentType, search]);

  if (isLoading) return <LoadingSkeleton type="card" />;
  if (error) return <ErrorBlock message="Failed to load definitions" detail={String(error)} />;
  if (!data || types.length === 0) {
    return (
      <div>
        <h1>Definitions</h1>
        <Card>
          <p style={{ color: 'var(--c-text-secondary)', margin: 0 }}>No definitions stored.</p>
        </Card>
      </div>
    );
  }

  return (
    <div>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: '1rem',
          gap: '0.75rem',
          flexWrap: 'wrap',
        }}
      >
        <h1 style={{ margin: 0 }}>Definitions</h1>
        <input
          type="text"
          placeholder="Search by name…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          style={{
            padding: '0.35rem 0.6rem',
            fontSize: '0.85rem',
            border: '1px solid var(--c-border)',
            borderRadius: 'var(--radius-sm)',
            width: 220,
            outline: 'none',
          }}
        />
      </div>

      {/* Type tabs */}
      <div
        role="tablist"
        aria-label="Definition types"
        style={{
          display: 'flex',
          gap: '0.25rem',
          flexWrap: 'wrap',
          marginBottom: '1rem',
        }}
        onKeyDown={(e) => {
          if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
          e.preventDefault();
          const idx = types.indexOf(currentType);
          const next = e.key === 'ArrowRight'
            ? (idx + 1) % types.length
            : (idx - 1 + types.length) % types.length;
          const t = types[next];
          if (t) setActiveType(t);
        }}
      >
        {types.map((t) => (
          <button
            key={t}
            type="button"
            role="tab"
            aria-selected={t === currentType}
            aria-controls="definitions-panel"
            onClick={() => setActiveType(t)}
            style={{
              padding: '0.3rem 0.75rem',
              fontSize: '0.82rem',
              borderRadius: 'var(--radius-md)',
              border: `1px solid ${t === currentType ? 'var(--c-primary)' : 'var(--c-border)'}`,
              background: t === currentType ? '#eef2ff' : 'var(--c-surface)',
              color: t === currentType ? 'var(--c-primary)' : 'var(--c-text)',
              cursor: 'pointer',
              fontWeight: t === currentType ? 600 : 400,
            }}
          >
            {t}
          </button>
        ))}
      </div>

      <div
        role="tabpanel"
        id="definitions-panel"
        aria-label={currentType}
      >
      {entries.length === 0 ? (
        <Card>
          <p style={{ color: 'var(--c-text-secondary)', margin: 0 }}>
            {search ? `No "${currentType}" definitions match your search.` : `No "${currentType}" definitions.`}
          </p>
        </Card>
      ) : (
        <Card padding={false}>
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
                <tr key={`${currentType}/${e.name}`}>
                  <td style={{ fontWeight: 500 }}>{e.name}</td>
                  <td>
                    <code>{e.version.slice(0, 12)}</code>
                  </td>
                  <td style={{ whiteSpace: 'nowrap' }}>{new Date(e.updated_at).toLocaleString()}</td>
                  <td>{e.size} B</td>
                  <td>
                    <Link
                      to={`/definitions/${encodeURIComponent(currentType)}/${encodeURIComponent(e.name)}`}
                    >
                      edit →
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
      </div>
    </div>
  );
}
