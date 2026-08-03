/**
 * Local ATS score history so users can see progress over time without a new API.
 */

export interface ATSHistoryEntry {
  at: string;
  overall: number;
  components: {
    ats: number;
    keywords: number;
    impact: number;
    role_fit: number;
    clarity: number;
  };
  target_role?: string;
  cv_version?: string;
}

const KEY = 'stawi.ats.score.history.v1';
const MAX = 20;

export function loadATSHistory(): ATSHistoryEntry[] {
  if (typeof window === 'undefined') return [];
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as ATSHistoryEntry[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export function pushATSHistory(entry: ATSHistoryEntry): ATSHistoryEntry[] {
  const prev = loadATSHistory();
  const next = [entry, ...prev].slice(0, MAX);
  try {
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    /* quota */
  }
  return next;
}
