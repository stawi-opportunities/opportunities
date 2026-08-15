import { useId, type ReactNode } from 'react';
import clsx from 'clsx';

interface FieldProps {
  label?: string;
  error?: string;
  hint?: string;
  children: (id: string) => ReactNode;
  className?: string;
}

export function Field({ label, error, hint, children, className }: FieldProps) {
  const id = useId();
  return (
    <div className={clsx('min-w-0', className)}>
      {label && (
        <label htmlFor={id} className="block text-sm font-medium text-main">
          {label}
        </label>
      )}
      <div className={label ? 'mt-1.5' : ''}>{children(id)}</div>
      {error ? (
        <p className="mt-1.5 text-sm text-red-600 dark:text-red-400" role="alert">
          {error}
        </p>
      ) : hint ? (
        <p className="mt-1.5 text-xs leading-relaxed text-secondary">{hint}</p>
      ) : null}
    </div>
  );
}
