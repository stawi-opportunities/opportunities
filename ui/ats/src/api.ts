/** API client — snake_case JSON. Dev headers until identity login is wired. */

export type Job = {
  id: string;
  title: string;
  description: string;
  location: string;
  status: string;
  visibility: string;
  opportunity_id?: string;
};

export type Application = {
  id: string;
  job_id: string;
  profile_id: string;
  candidate_id?: string;
  stage: string;
  source: string;
  status: string;
  summary?: string;
  score?: number;
  job_title?: string;
};

export type TalentHit = {
  profile_id: string;
  candidate_id?: string;
  score?: number;
  summary?: string;
};

export type Interview = {
  id: string;
  application_id: string;
  type: string;
  duration_min: number;
  panel: string[];
  status: string;
  slot_start?: string;
  slot_end?: string;
  job_title?: string;
  candidate_profile_id?: string;
};

export type Slot = { start: string; end: string };

export type Dashboard = {
  open_jobs: number;
  active_applications: number;
  interviews_this_week: number;
  upcoming_interviews: Interview[];
  needs_attention: string[];
};

export type Availability = {
  profile_id: string;
  timezone: string;
  rules: { weekday: number; start: string; end: string }[];
  exceptions: { date: string; blocked: boolean }[];
};

const headers = (): HeadersInit => ({
  "Content-Type": "application/json",
  "X-Profile-ID": localStorage.getItem("ats_profile_id") || "dev-recruiter",
  "X-Tenant-ID": localStorage.getItem("ats_tenant_id") || "dev-tenant",
  "X-Partition-ID": localStorage.getItem("ats_partition_id") || "dev-partition",
});

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { ...headers(), ...(init?.headers || {}) },
  });
  if (!res.ok) {
    let detail = await res.text();
    try {
      const j = JSON.parse(detail);
      detail = j.detail || j.title || detail;
    } catch {
      /* keep text */
    }
    throw new Error(detail || `${res.status}`);
  }
  if (res.status === 204) return undefined as T;
  const ct = res.headers.get("content-type") || "";
  if (ct.includes("text/calendar")) {
    return (await res.text()) as T;
  }
  return res.json() as Promise<T>;
}

export const api = {
  dashboard: () => req<Dashboard>("/v1/dashboard"),
  seed: () => req<{ seeded: boolean }>("/v1/demo/seed", { method: "POST" }),
  listJobs: () => req<{ jobs: Job[] }>("/v1/jobs"),
  createJob: (title: string, description: string, location: string) =>
    req<Job>("/v1/jobs", {
      method: "POST",
      body: JSON.stringify({ title, description, location, status: "open" }),
    }),
  publishJob: (id: string) => req<Job>(`/v1/jobs/${id}/publish`, { method: "POST" }),
  unpublishJob: (id: string) => req<Job>(`/v1/jobs/${id}/unpublish`, { method: "POST" }),
  closeJob: (id: string) => req<Job>(`/v1/jobs/${id}/close`, { method: "POST" }),
  listApplications: (jobId: string) =>
    req<{ applications: Application[] }>(`/v1/jobs/${jobId}/applications`),
  createApplication: (jobId: string, profileId: string, summary?: string) =>
    req<Application>(`/v1/jobs/${jobId}/applications`, {
      method: "POST",
      body: JSON.stringify({ profile_id: profileId, summary }),
    }),
  listTalent: (jobId: string) => req<{ talent: TalentHit[] }>(`/v1/jobs/${jobId}/talent`),
  addTalent: (jobId: string, hit: TalentHit) =>
    req<Application>(`/v1/jobs/${jobId}/talent`, {
      method: "POST",
      body: JSON.stringify(hit),
    }),
  advance: (appId: string, toStage: string) =>
    req<Application>(`/v1/applications/${appId}/advance`, {
      method: "POST",
      body: JSON.stringify({ to_stage: toStage }),
    }),
  hire: (appId: string) =>
    req<{ application: Application }>(`/v1/applications/${appId}/hire`, { method: "POST" }),
  screenSummary: (appId: string) =>
    req<{ summary: string }>(`/v1/ai/applications/${appId}/screen-summary`, { method: "POST" }),
  proposeInterview: (appId: string, durationMin = 30) =>
    req<Interview>(`/v1/applications/${appId}/interviews`, {
      method: "POST",
      body: JSON.stringify({ duration_min: durationMin, type: "screen" }),
    }),
  listSlots: (interviewId: string) =>
    req<{ slots: Slot[] }>(`/v1/interviews/${interviewId}/slots`),
  bookInterview: (interviewId: string, start: string, end: string) =>
    req<Interview>(`/v1/interviews/${interviewId}/book`, {
      method: "POST",
      body: JSON.stringify({ start, end }),
    }),
  getAvailability: () => req<Availability | { availability: null }>("/v1/me/availability"),
  setAvailability: (body: { timezone: string; rules: Availability["rules"] }) =>
    req<Availability>("/v1/me/availability", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  icsUrl: (interviewId: string) => `/v1/interviews/${interviewId}/ics`,
};

export const NEXT_STAGE: Record<string, string> = {
  applied: "screen",
  screen: "interview",
  interview: "offer",
  offer: "hired",
};

export const STAGE_LABEL: Record<string, string> = {
  applied: "Applied",
  screen: "Screen",
  interview: "Interview",
  offer: "Offer",
  hired: "Hired",
  rejected: "Rejected",
  withdrawn: "Withdrawn",
};
