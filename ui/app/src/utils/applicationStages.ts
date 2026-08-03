/**
 * Client-side application stage overrides until full applications service
 * status is wired into the dashboard feed (feed only exposes status summary).
 */

export type ApplicationStage =
  | 'applied'
  | 'responded'
  | 'interview'
  | 'offer'
  | 'rejected'
  | 'hired';

export const STAGES: { id: ApplicationStage; label: string }[] = [
  { id: 'applied', label: 'Applied' },
  { id: 'responded', label: 'Responded' },
  { id: 'interview', label: 'Interview' },
  { id: 'offer', label: 'Offer' },
  { id: 'rejected', label: 'Rejected' },
  { id: 'hired', label: 'Hired' },
];

const KEY = 'stawi.application.stage.overrides.v1';

export function loadStageOverrides(): Record<string, ApplicationStage> {
  if (typeof window === 'undefined') return {};
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return {};
    return JSON.parse(raw) as Record<string, ApplicationStage>;
  } catch {
    return {};
  }
}

export function setStageOverride(opportunityId: string, stage: ApplicationStage): void {
  const all = loadStageOverrides();
  all[opportunityId] = stage;
  try {
    localStorage.setItem(KEY, JSON.stringify(all));
  } catch {
    /* quota */
  }
}

export function resolveStage(
  opportunityId: string,
  serverStatus?: string
): ApplicationStage | string {
  const o = loadStageOverrides()[opportunityId];
  if (o) return o;
  return serverStatus || 'applied';
}
