import type { ButtonHTMLAttributes, ReactNode } from 'react';

type Variant = 'primary' | 'danger' | 'ghost' | 'outline';
type Size = 'sm' | 'md';

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  children: ReactNode;
}

const base: React.CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  gap: '0.4rem',
  fontWeight: 500,
  fontSize: '0.85rem',
  lineHeight: 1.4,
  cursor: 'pointer',
  border: '1px solid transparent',
  borderRadius: 'var(--radius-md)',
  transition: 'background 0.15s, border-color 0.15s, opacity 0.15s',
};

const variants: Record<Variant, React.CSSProperties> = {
  primary: { backgroundColor: 'var(--c-primary)', color: '#fff', borderColor: 'var(--c-primary)' },
  danger: { backgroundColor: 'var(--c-danger)', color: '#fff', borderColor: 'var(--c-danger)' },
  ghost: { backgroundColor: 'transparent', color: 'var(--c-text)', borderColor: 'transparent' },
  outline: { backgroundColor: 'transparent', color: 'var(--c-text)', borderColor: 'var(--c-border)' },
};

const sizes: Record<Size, React.CSSProperties> = {
  sm: { padding: '0.2rem 0.5rem', fontSize: '0.8rem' },
  md: { padding: '0.35rem 0.8rem' },
};

export function Button({
  variant = 'primary',
  size = 'md',
  loading = false,
  disabled,
  children,
  style,
  ...rest
}: ButtonProps) {
  const hoverBg: Record<Variant, string> = {
    primary: 'var(--c-primary-hover)',
    danger: 'var(--c-danger-hover)',
    ghost: '#eef0f2',
    outline: '#eef0f2',
  };

  return (
    <button
      style={{
        ...base,
        ...variants[variant],
        ...sizes[size],
        opacity: disabled || loading ? 0.55 : undefined,
        pointerEvents: disabled || loading ? 'none' : undefined,
        ...style,
      }}
      onMouseEnter={(e) => {
        if (!disabled && !loading)
          (e.currentTarget as HTMLButtonElement).style.backgroundColor = hoverBg[variant];
      }}
      onMouseLeave={(e) => {
        if (!disabled && !loading)
          (e.currentTarget as HTMLButtonElement).style.backgroundColor = variants[variant].backgroundColor || '';
      }}
      disabled={disabled || loading}
      {...rest}
    >
      {loading && <Spinner />}
      {children}
    </button>
  );
}

function Spinner() {
  return (
    <span
      style={{
        display: 'inline-block',
        width: '0.75em',
        height: '0.75em',
        border: '2px solid currentColor',
        borderTopColor: 'transparent',
        borderRadius: '50%',
        animation: 'spin 0.5s linear infinite',
      }}
    />
  );
}
