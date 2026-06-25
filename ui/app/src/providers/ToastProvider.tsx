import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { useToastStore, type Toast } from '@/stores/toastStore';

// Only the first ToastProvider to commit a layout effect renders the
// visible container. Remaining providers on the same page are
// transparent pass-throughs. This prevents duplicate toast stacks
// when multiple React islands share a page (each wrapped in
// AppProviders) while keeping the zustand store accessible from
// every island via useToast().
let _containerOwner: symbol | null = null;

const VARIANT_CLASSES: Record<Toast['variant'], string> = {
  success: 'border-emerald-200 bg-emerald-50 text-emerald-900',
  error: 'border-red-200   bg-red-50   text-red-900',
  warning: 'border-amber-200 bg-amber-50 text-amber-900',
  info: 'border-blue-200  bg-blue-50  text-blue-900',
};

const ICON: Record<Toast['variant'], string> = {
  success: '✓',
  error: '✕',
  warning: '⚠',
  info: 'ℹ',
};

const AUTO_DISMISS_MS = 4_000;

function ToastItem({ toast, onDismiss }: { toast: Toast; onDismiss: (id: string) => void }) {
  const [exiting, setExiting] = useState(false);

  useEffect(() => {
    const timer = setTimeout(() => {
      setExiting(true);
      setTimeout(() => onDismiss(toast.id), 200);
    }, AUTO_DISMISS_MS);
    return () => clearTimeout(timer);
  }, [toast.id, onDismiss]);

  return (
    <div
      role="alert"
      aria-live="assertive"
      className={`flex items-start gap-3 rounded-lg border p-4 shadow-md transition-all duration-200 ${
        exiting ? 'translate-x-full opacity-0' : 'translate-x-0 opacity-100'
      } ${VARIANT_CLASSES[toast.variant]}`}
    >
      <span className="mt-0.5 shrink-0 text-sm font-bold" aria-hidden="true">
        {ICON[toast.variant]}
      </span>
      <p className="flex-1 text-sm">{toast.message}</p>
      <button
        type="button"
        onClick={() => onDismiss(toast.id)}
        aria-label="Dismiss notification"
        className="shrink-0 rounded p-0.5 opacity-60 hover:opacity-100 focus:outline-none focus:ring-2 focus:ring-current"
      >
        <svg
          className="h-4 w-4"
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
  );
}

function ToastStack() {
  const toasts = useToastStore((s) => s.toasts);
  const dismiss = useToastStore((s) => s.dismiss);

  if (toasts.length === 0) return null;

  return (
    <div
      className="pointer-events-none fixed bottom-4 right-4 z-[9999] flex w-full max-w-sm flex-col gap-2"
      aria-label="Notifications"
    >
      {toasts.map((t) => (
        <div key={t.id} className="pointer-events-auto">
          <ToastItem toast={t} onDismiss={dismiss} />
        </div>
      ))}
    </div>
  );
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const myToken = useRef(Symbol());
  const [isOwner, setIsOwner] = useState(false);

  useLayoutEffect(() => {
    if (_containerOwner === null) {
      _containerOwner = myToken.current;
      setIsOwner(true);
    }
    return () => {
      if (_containerOwner === myToken.current) {
        _containerOwner = null;
      }
    };
  }, []);

  return (
    <>
      {children}
      {isOwner && typeof document !== 'undefined'
        ? createPortal(<ToastStack />, document.body)
        : null}
    </>
  );
}
