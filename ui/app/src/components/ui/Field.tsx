import { useId, type ReactNode } from 'react';
import clsx from 'clsx';

interface FieldProps {
  label?: string;
  error?: string;
  children: (id: string) => ReactNode;
  className?: string;
}

export function Field({ label, error, children, className }: FieldProps) {
  const id = useId();
  return (
    <div className={clsx(className)}>
      {label && (
        <label htmlFor={id} className="block text-sm font-medium text-gray-700 dark:text-gray-300">
          {label}
        </label>
      )}
      <div className={label ? 'mt-1' : ''}>{children(id)}</div>
      {error && (
        <p className="mt-1 text-sm text-red-600 dark:text-red-400" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
