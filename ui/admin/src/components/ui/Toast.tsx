import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react';

type ToastType = 'success' | 'error' | 'info' | 'warning';

interface ToastItem {
  id: number;
  message: string;
  type: ToastType;
  leaving?: boolean;
}

interface ToastContextValue {
  toast: (message: string, opts?: { type?: ToastType; duration?: number }) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

let nextId = 1;
const MAX_VISIBLE = 5;

const typeColors: Record<ToastType, { bg: string; border: string; text: string }> = {
  success: { bg: '#e6f7ed', border: '#b7ebc8', text: '#166534' },
  error: { bg: '#fef2f2', border: '#fecaca', text: '#991b1b' },
  info: { bg: '#e0f5f3', border: '#b3e0db', text: '#115e59' },
  warning: { bg: '#fef4e2', border: '#fde68a', text: '#92400e' },
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const timersRef = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map());

  const remove = useCallback((id: number) => {
    setItems((prev) => prev.map((t) => t.id === id ? { ...t, leaving: true } : t));
    const existing = timersRef.current.get(id);
    if (existing) clearTimeout(existing);
    const timer = setTimeout(() => {
      setItems((prev) => prev.filter((t) => t.id !== id));
      timersRef.current.delete(id);
    }, 200);
    timersRef.current.set(id, timer);
  }, []);

  const toast = useCallback((message: string, opts?: { type?: ToastType; duration?: number }) => {
    const id = nextId++;
    const type = opts?.type ?? 'info';
    const duration = opts?.duration ?? 4000;
    setItems((prev) => {
      const next = [...prev, { id, message, type }];
      return next.length > MAX_VISIBLE ? next.slice(next.length - MAX_VISIBLE) : next;
    });
    const timer = setTimeout(() => remove(id), duration);
    timersRef.current.set(id, timer);
  }, [remove]);

  return (
    <ToastContext.Provider value={{ toast }}>
      {children}
      <div
        aria-live="polite"
        aria-relevant="additions"
        style={{
          position: 'fixed',
          top: '0.75rem',
          right: '0.75rem',
          zIndex: 2000,
          display: 'flex',
          flexDirection: 'column',
          gap: '0.5rem',
          maxWidth: 360,
        }}
      >
        {items.map((item) => {
          const c = typeColors[item.type];
          return (
            <div
              key={item.id}
              role="alert"
              style={{
                background: c.bg,
                border: `1px solid ${c.border}`,
                borderRadius: 'var(--radius-md)',
                padding: '0.6rem 1rem',
                color: c.text,
                fontSize: '0.85rem',
                boxShadow: 'var(--shadow-md)',
                animation: item.leaving ? 'toast-out 0.2s ease-in forwards' : 'toast-in 0.25s ease-out',
                display: 'flex',
                alignItems: 'center',
                gap: '0.5rem',
                cursor: 'pointer',
              }}
              onClick={() => remove(item.id)}
            >
              <span style={{ flex: 1 }}>{item.message}</span>
              <span style={{ opacity: 0.5, fontSize: '0.8rem' }}>×</span>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('useToast must be used within <ToastProvider>');
  return ctx;
}
