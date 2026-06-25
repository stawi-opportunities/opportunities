import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { getOpportunityTrace } from '@/api/admin-client';
import { Card, ErrorBlock, LoadingSkeleton, StatusBadge } from '@/components/ui';

type SortKey = 'ingested_at' | 'joined_at' | 'source';
type SortDir = 'asc' | 'desc';

export function OpportunityTrace() {
  const { slug } = useParams<{ slug: string }>();
  const [sortKey, setSortKey] = useState<SortKey>('joined_at');
  const [sortDir, setSortDir] = useState<SortDir>('desc');

  const { data, isLoading, error } = useQuery({
    queryKey: ['opportunity-trace', slug],
    queryFn: () => getOpportunityTrace(slug ?? ''),
    enabled: !!slug,
  });

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(key);
      setSortDir('desc');
    }
  };

  if (!slug) return <ErrorBlock message="Missing opportunity slug" />;
  if (isLoading) return <LoadingSkeleton type="card" />;
  if (error) return <ErrorBlock message="Failed to load opportunity" detail={String(error)} />;
  if (!data) return null;

  const sorted = [...data.variants].sort((a, b) => {
    const dir = sortDir === 'asc' ? 1 : -1;
    if (sortKey === 'source') return a.source.id.localeCompare(b.source.id) * dir;
    return (new Date(a[sortKey]).getTime() - new Date(b[sortKey]).getTime()) * dir;
  });

  const SortIcon = () => <span style={{ fontSize: '0.7rem', marginLeft: '0.2rem' }} aria-hidden="true">{sortDir === 'asc' ? '▲' : '▼'}</span>;

  return (
    <div>
      <div style={{ marginBottom: '1.25rem' }}>
        <h1 style={{ margin: 0 }}>{data.slug}</h1>
        <p style={{ margin: '0.25rem 0 0', color: 'var(--c-text-secondary)', fontSize: '0.88rem' }}>
          {data.variant_count} variant(s) joined this canonical.
        </p>
      </div>

      {sorted.length === 0 ? (
        <Card>
          <p style={{ color: 'var(--c-text-secondary)', margin: 0 }}>
            No variants in the 7-day Postgres retention window.
          </p>
        </Card>
      ) : (
        <Card padding={false}>
          <table>
            <thead>
              <tr>
                <th>Variant</th>
                <th
                  tabIndex={0}
                  role="columnheader"
                  aria-sort={sortKey === 'source' ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'}
                  style={{ cursor: 'pointer', userSelect: 'none' }}
                  onClick={() => toggleSort('source')}
                  onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleSort('source'); } }}
                >
                  Source{sortKey === 'source' && <SortIcon />}
                </th>
                <th
                  tabIndex={0}
                  role="columnheader"
                  aria-sort={sortKey === 'ingested_at' ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'}
                  style={{ cursor: 'pointer', userSelect: 'none' }}
                  onClick={() => toggleSort('ingested_at')}
                  onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleSort('ingested_at'); } }}
                >
                  Ingested{sortKey === 'ingested_at' && <SortIcon />}
                </th>
                <th
                  tabIndex={0}
                  role="columnheader"
                  aria-sort={sortKey === 'joined_at' ? (sortDir === 'asc' ? 'ascending' : 'descending') : 'none'}
                  style={{ cursor: 'pointer', userSelect: 'none' }}
                  onClick={() => toggleSort('joined_at')}
                  onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggleSort('joined_at'); } }}
                >
                  Joined{sortKey === 'joined_at' && <SortIcon />}
                </th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((v) => (
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
                    <StatusBadge variant="neutral" label={v.source.type} size="sm" dot={false} />
                  </td>
                  <td style={{ whiteSpace: 'nowrap' }}>{new Date(v.ingested_at).toLocaleString()}</td>
                  <td style={{ whiteSpace: 'nowrap' }}>{new Date(v.joined_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
    </div>
  );
}
