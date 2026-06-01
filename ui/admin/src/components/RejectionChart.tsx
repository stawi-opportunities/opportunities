// RejectionChart renders a Record<reason, count> as a horizontal bar
// chart using pure CSS — no external chart library so the admin bundle
// stays small and CSP-safe. Sorted descending by count so the most
// common rejection reason is at the top.
export function RejectionChart({ reasons }: { reasons: Record<string, number> }) {
  const entries = Object.entries(reasons).sort((a, b) => b[1] - a[1]);
  if (entries.length === 0) {
    return <p style={{ color: '#666' }}>No rejections in the window.</p>;
  }
  const max = Math.max(1, ...entries.map(([, n]) => n));
  return (
    <table style={{ tableLayout: 'fixed' }}>
      <tbody>
        {entries.map(([reason, count]) => (
          <tr key={reason}>
            <td style={{ width: '220px', verticalAlign: 'middle' }}>
              <code>{reason}</code>
            </td>
            <td>
              <div
                style={{
                  background: '#c00',
                  width: `${(count / max) * 100}%`,
                  minWidth: '2rem',
                  padding: '0.2rem 0.4rem',
                  color: 'white',
                  fontSize: '0.85rem',
                  borderRadius: '2px',
                }}
              >
                {count}
              </div>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
