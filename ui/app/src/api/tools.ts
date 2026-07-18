import { authRuntime } from '@/auth/runtime';

export interface CVScoreComponents {
  ats: number;
  keywords: number;
  impact: number;
  role_fit: number;
  clarity: number;
}

export interface PriorityFix {
  id: string;
  title: string;
  impact: string;
  category: string;
  why: string;
  auto_applicable: boolean;
  suggestions?: string[];
}

export interface CVStrengthReport {
  overall_score: number;
  components: CVScoreComponents;
  target_role: string;
  role_family: string;
  priority_fixes: PriorityFix[];
  rewrites?: { before: string; after: string; reason: string }[];
  generated_at: string;
  cv_version: string;
}

export interface JobFitResult {
  score: number;
  label: string;
  signals: string[];
  suggestions: string[];
  title?: string;
}

/** POST /matching/me/tools/cv-score — free ATS-style CV report. */
export async function scoreCV(input: {
  target_role?: string;
  cv_text?: string;
}): Promise<CVStrengthReport> {
  return authRuntime().fetch('/matching/me/tools/cv-score', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
    timeoutMs: 60_000,
  });
}

/** POST /matching/me/tools/job-fit — free keyword fitness vs a job description. */
export async function fitJob(input: {
  job_text?: string;
  opportunity_id?: string;
  title?: string;
}): Promise<JobFitResult> {
  return authRuntime().fetch('/matching/me/tools/job-fit', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
    timeoutMs: 30_000,
  });
}
