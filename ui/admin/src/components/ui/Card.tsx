import type { ReactNode } from 'react';

interface CardProps {
  title?: string;
  children: ReactNode;
  footer?: ReactNode;
  padding?: boolean;
}

export function Card({ title, children, footer, padding = true }: CardProps) {
  return (
    <section
      style={{
        background: 'var(--c-surface)',
        border: '1px solid var(--c-border)',
        borderRadius: 'var(--radius-lg)',
        boxShadow: 'var(--shadow-sm)',
        animation: 'slideUp 0.3s ease-out',
      }}
    >
      {title && (
        <div
          style={{
            padding: padding ? '0.75rem 1rem 0' : '0 1rem',
            fontSize: '0.95rem',
            fontWeight: 600,
          }}
        >
          {title}
        </div>
      )}
      <div style={{ padding: padding ? '1rem' : undefined }}>
        {children}
      </div>
      {footer && (
        <div
          style={{
            padding: '0.6rem 1rem',
            borderTop: '1px solid var(--c-border)',
            background: '#f9fafb',
            borderRadius: '0 0 var(--radius-lg) var(--radius-lg)',
          }}
        >
          {footer}
        </div>
      )}
    </section>
  );
}
