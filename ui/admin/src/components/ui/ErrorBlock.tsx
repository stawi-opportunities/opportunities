import { useState } from 'react';

interface ErrorBlockProps {
  message: string;
  detail?: string;
  onRetry?: () => void;
}

export function ErrorBlock({ message, detail, onRetry }: ErrorBlockProps) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div
      role="alert"
      aria-live="assertive"
      style={{
        background: '#fef2f2',
        border: '1px solid #fecaca',
        borderRadius: 'var(--radius-md)',
        padding: '0.75rem 1rem',
        color: '#991b1b',
        fontSize: '0.9rem',
        animation: 'fadeIn 0.25s ease-in',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
        <span style={{ fontWeight: 600, flex: 1 }}>{message}</span>
        {onRetry && (
          <button
            type="button"
            onClick={onRetry}
            style={{
              background: 'none',
              border: '1px solid #fecaca',
              borderRadius: 'var(--radius-sm)',
              padding: '0.2rem 0.6rem',
              cursor: 'pointer',
              fontSize: '0.8rem',
              color: '#991b1b',
            }}
          >
            Retry
          </button>
        )}
        {detail && (
          <button
            type="button"
            aria-expanded={expanded}
            onClick={() => setExpanded(!expanded)}
            style={{
              background: 'none',
              border: 'none',
              cursor: 'pointer',
              fontSize: '0.8rem',
              color: '#991b1b',
              textDecoration: 'underline',
            }}
          >
            {expanded ? 'Hide details' : 'Details'}
          </button>
        )}
      </div>
      {expanded && detail && (
        <pre
          style={{
            marginTop: '0.5rem',
            fontSize: '0.8rem',
            whiteSpace: 'pre-wrap',
            background: '#fff',
            padding: '0.5rem',
            borderRadius: 'var(--radius-sm)',
            border: '1px solid #fecaca',
            overflowX: 'auto',
          }}
        >
          {detail}
        </pre>
      )}
    </div>
  );
}
