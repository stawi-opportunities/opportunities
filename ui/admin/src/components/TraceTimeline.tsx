// TraceTimeline renders the ordered stages of a single variant's lifecycle
// (ingested → enriched → canonicalised → published, or → rejected) as a
// vertical timeline with a coloured marker per node. Used by VariantTrace
// today; the same shape is reused by any future per-record audit view.
type Stage = {
  stage: string;
  at: string;
  duration_ms?: number;
  canonical_id?: string;
};

function stageColor(stage: string): string {
  if (stage === 'rejected' || stage === 'failed') return 'var(--c-danger)';
  if (stage === 'published') return 'var(--c-success)';
  return 'var(--c-info)';
}

export function TraceTimeline({ stages }: { stages: Stage[] }) {
  if (stages.length === 0) {
    return <p style={{ color: 'var(--c-text-secondary)' }}>No stage transitions recorded.</p>;
  }
  return (
    <ol
      style={{
        listStyle: 'none',
        padding: 0,
        margin: '0.5rem 0 0 0.5rem',
        borderLeft: '2px solid var(--c-border)',
      }}
    >
      {stages.map((s, i) => (
        <li
          key={`${s.stage}-${s.at}-${i}`}
          style={{
            padding: '0.4rem 0 0.4rem 1rem',
            position: 'relative',
          }}
        >
          <span
            style={{
              position: 'absolute',
              left: '-7px',
              top: '0.7rem',
              width: '12px',
              height: '12px',
              borderRadius: '50%',
              background: stageColor(s.stage),
            }}
          />
          <strong>{s.stage}</strong>{' '}
          <small style={{ color: 'var(--c-text-secondary)' }}>
            at {new Date(s.at).toLocaleString()}
          </small>
          {s.duration_ms != null && s.duration_ms > 0 && (
            <span style={{ marginLeft: '0.5rem', color: 'var(--c-text-secondary)' }}>
              ({s.duration_ms} ms)
            </span>
          )}
          {s.canonical_id && (
            <span style={{ marginLeft: '0.5rem', color: 'var(--c-text-secondary)' }}>
              canonical=<code>{s.canonical_id}</code>
            </span>
          )}
        </li>
      ))}
    </ol>
  );
}
