import { authRuntime } from '@/auth/runtime';

export interface JobFitResult {
  score: number;
  label: string;
  signals: string[];
  suggestions: string[];
  title?: string;
  /** "keywords" | "vector+stored" | "vector+live" */
  method?: string;
  vector_score?: number;
  keyword_score?: number;
}

/** Paid $2 comprehensive ATS report vs matched jobs — emailed after checkout. */
export interface ATSReportCheckoutResponse {
  ok: boolean;
  product_id: string;
  amount_usd: number;
  usd_cents: number;
  currency: string;
  status: string;
  redirect_url?: string;
  prompt_id?: string;
  message?: string;
}

/**
 * POST /matching/me/tools/ats-report — start $2 checkout for match-aware ATS report.
 * After payment, the report is emailed as an HTML attachment.
 */
export async function purchaseATSReport(): Promise<ATSReportCheckoutResponse> {
  return authRuntime().fetch('/matching/me/tools/ats-report', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{}',
    timeoutMs: 60_000,
  });
}

/** POST /matching/me/tools/job-fit — free vector+keyword fitness vs a job description. */
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
