import { useEffect, useState, type ReactNode } from 'react';
import { getRoles } from '@/api/admin-client';

// Role-based mount per the admin-trace plan.
//
// The API's requireAdmin is the actual security boundary — this gate is
// defense-in-depth + a UX nicety so non-admins don't see a half-rendered
// page that 403s on every request. Renders a 404-shaped page on denial
// to avoid leaking the existence of the admin surface to non-admins.
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

  if (state === 'checking') return <p>Loading…</p>;
  if (state === 'denied')
    return (
      <div style={{ padding: '2rem', textAlign: 'center' }}>
        <h1>Not found</h1>
        <p>This page does not exist.</p>
      </div>
    );
  return <>{children}</>;
}
