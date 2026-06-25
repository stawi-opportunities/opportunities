type BadgeVariant = 'success' | 'warning' | 'error' | 'info' | 'neutral';

interface StatusBadgeProps {
  variant: BadgeVariant;
  label: string;
  dot?: boolean;
  size?: 'sm' | 'md';
}

const dotColors: Record<BadgeVariant, string> = {
  success: 'var(--c-success)',
  warning: 'var(--c-warning)',
  error: 'var(--c-danger)',
  info: 'var(--c-info)',
  neutral: 'var(--c-neutral)',
};

const bgColors: Record<BadgeVariant, string> = {
  success: '#e6f7ed',
  warning: '#fef4e2',
  error: '#fde8e8',
  info: '#e0f5f3',
  neutral: '#f0f1f3',
};

export function StatusBadge({ variant, label, dot = true, size = 'md' }: StatusBadgeProps) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '0.3rem',
        padding: size === 'sm' ? '0.1rem 0.4rem' : '0.15rem 0.5rem',
        fontSize: size === 'sm' ? '0.78rem' : '0.85rem',
        fontWeight: 500,
        borderRadius: 'var(--radius-sm)',
        background: bgColors[variant],
        color: dotColors[variant],
        whiteSpace: 'nowrap',
      }}
    >
      {dot && (
        <span
          style={{
            width: size === 'sm' ? 6 : 8,
            height: size === 'sm' ? 6 : 8,
            borderRadius: '50%',
            background: dotColors[variant],
            flexShrink: 0,
          }}
        />
      )}
      {label}
    </span>
  );
}
