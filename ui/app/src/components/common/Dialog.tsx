import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useFocusTrap } from '@/hooks/useFocusTrap';
import clsx from 'clsx';

interface DialogProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  description?: string;
}

export function Dialog({ open, onClose, title, description, children }: DialogProps) {
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const titleId = useRef(`dialog-title-${crypto.randomUUID()}`);
  const descId = useRef(`dialog-desc-${crypto.randomUUID()}`);
  const [exiting, setExiting] = useState(false);

  useFocusTrap(dialogRef, open, onClose);

  useEffect(() => {
    if (!open) return;
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', handleKey);
    return () => document.removeEventListener('keydown', handleKey);
  }, [open, onClose]);

  useEffect(() => {
    if (open) {
      document.body.style.overflow = 'hidden';
      setExiting(false);
    }
    return () => {
      document.body.style.overflow = '';
    };
  }, [open]);

  const handleClose = () => {
    setExiting(true);
    setTimeout(() => {
      setExiting(false);
      onClose();
    }, 200);
  };

  if (!open && !exiting) return null;

  return (
    <div
      className={clsx(
        'fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4 transition-opacity duration-200',
        exiting ? 'opacity-0' : 'opacity-100 animate-fade-in',
      )}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId.current}
      aria-describedby={description ? descId.current : undefined}
      onClick={(e) => {
        if (e.target === e.currentTarget) handleClose();
      }}
    >
      <div
        ref={dialogRef}
        className={clsx(
          'w-full max-w-md max-h-[90vh] overflow-y-auto rounded-lg bg-white p-6 shadow-xl transition-all duration-200 dark:bg-navy-900',
          exiting ? 'scale-95 opacity-0 translate-y-2' : 'scale-100 opacity-100 animate-slide-down',
        )}
      >
        <div className="mb-4 flex items-start justify-between gap-4">
          <div>
            <h2
              id={titleId.current}
              className="text-lg font-semibold text-gray-900 dark:text-white"
            >
              {title}
            </h2>
            {description && (
              <p id={descId.current} className="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {description}
              </p>
            )}
          </div>
          <button
            type="button"
            onClick={handleClose}
            aria-label="Close dialog"
            className="shrink-0 rounded p-1 text-gray-500 hover:text-gray-600 focus:outline-none focus:ring-2 focus:ring-navy-500 dark:text-gray-400 dark:hover:text-gray-300"
          >
            <svg
              className="h-5 w-5"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              aria-hidden="true"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M6 18L18 6M6 6l12 12"
              />
            </svg>
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
