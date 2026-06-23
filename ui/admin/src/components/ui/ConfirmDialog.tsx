import { useEffect, useRef } from 'react';
import { useFocusTrap } from '@/hooks/useFocusTrap';
import { useEscapeKey } from '@/hooks/useEscapeKey';
import { useScrollLock } from '@/hooks/useScrollLock';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  variant?: 'danger' | 'primary';
  onConfirm: () => void;
  onCancel: () => void;
  busy?: boolean;
}

export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  variant = 'danger',
  onConfirm,
  onCancel,
  busy = false,
}: ConfirmDialogProps) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);

  useFocusTrap(dialogRef, open, onCancel);
  useEscapeKey(onCancel, open);
  useScrollLock(open);

  useEffect(() => {
    if (open) {
      confirmRef.current?.focus();
    }
  }, [open]);

  if (!open) return null;

  return (
    <div
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 1000,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        animation: 'fadeIn 0.15s ease-out',
      }}
    >
      <div
        style={{
          position: 'absolute',
          inset: 0,
          background: 'rgba(0,0,0,0.35)',
        }}
        onClick={(e) => {
          if (e.target === e.currentTarget) onCancel();
        }}
      />
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        aria-describedby="confirm-message"
        style={{
          position: 'relative',
          background: 'var(--c-surface)',
          borderRadius: 'var(--radius-lg)',
          boxShadow: '0 8px 30px rgba(0,0,0,0.18)',
          padding: '1.25rem',
          maxWidth: 420,
          width: '90%',
          animation: 'slideUp 0.2s ease-out',
        }}
      >
        <h2
          id="confirm-title"
          style={{ margin: '0 0 0.5rem', fontSize: '1.05rem' }}
        >
          {title}
        </h2>

        <p
          id="confirm-message"
          style={{
            margin: 0,
            color: 'var(--c-text-secondary)',
            fontSize: '0.9rem',
          }}
        >
          {message}
        </p>

        <div
          style={{
            marginTop: '1.25rem',
            display: 'flex',
            gap: '0.5rem',
            justifyContent: 'flex-end',
          }}
        >
          <button
            type="button"
            onClick={onCancel}
            disabled={busy}
            style={{
              padding: '0.35rem 0.8rem',
              borderRadius: 'var(--radius-md)',
              border: '1px solid var(--c-border)',
              background: 'var(--c-surface)',
              cursor: 'pointer',
              fontSize: '0.85rem',
            }}
          >
            {cancelLabel}
          </button>

          <button
            ref={confirmRef}
            type="button"
            onClick={onConfirm}
            disabled={busy}
            style={{
              padding: '0.35rem 0.8rem',
              borderRadius: 'var(--radius-md)',
              border: 'none',
              background:
                variant === 'danger'
                  ? 'var(--c-danger)'
                  : 'var(--c-primary)',
              color: '#fff',
              cursor: 'pointer',
              fontSize: '0.85rem',
              fontWeight: 500,
              opacity: busy ? 0.6 : undefined,
            }}
          >
            {busy ? 'Processing…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}