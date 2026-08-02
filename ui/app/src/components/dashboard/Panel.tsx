import type { ReactNode } from 'react';

export function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="rounded-lg border border-muted bg-surface p-4 sm:p-6">
      <h2 className="text-lg font-semibold text-main">{title}</h2>
      <div className="mt-2">{children}</div>
    </div>
  );
}
