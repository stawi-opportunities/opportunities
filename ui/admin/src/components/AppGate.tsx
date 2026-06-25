import { useEffect, useState, type ReactNode } from 'react';
import { getRoles } from '@/api/admin-client';
import { LoadingSkeleton } from '@/components/ui';

export function AppGate({ children }: { children: ReactNode }) {
  const [state, setState] = useState<'checking' | 'ok' | 'denied'>('checking');

  useEffect(() => {
    let cancelled = false;
    getRoles()
      .then((roles) => {
        if (cancelled) return;
        setState(roles.includes('admin') ? 'ok' : 'denied');
      })
      .catch(() => {
        if (!cancelled) setState('denied');
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (state === 'checking') return <LoadingSkeleton type="card" />;
  if (state === 'denied')
    return (
      <div style={{ padding: '2rem', textAlign: 'center' }}>
        <h1>Access denied</h1>
        <p>You do not have admin permissions.</p>
      </div>
    );
  return <>{children}</>;
}
