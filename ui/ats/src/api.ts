/**
 * Connect-protocol client for ats.v1.AtsService (JSON).
 * Auth via authFetchJson (OIDC runtime or local dev headers).
 */

import { authFetchJson } from "./auth";

const SERVICE = "/ats.v1.AtsService";

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

function newIdempotencyKey(prefix: string): string {
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;
}

async function rpc<TReq extends object, TRes>(
  method: string,
  body: TReq,
  opts?: { idempotencyKey?: string },
): Promise<TRes> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "Connect-Protocol-Version": "1",
  };
  if (opts?.idempotencyKey) {
    headers["Idempotency-Key"] = opts.idempotencyKey;
  }
  return authFetchJson<TRes>(`${SERVICE}/${method}`, {
    method: "POST",
    headers,
    body: JSON.stringify(body ?? {}),
  });
}

export const api = {
  dashboard: async () => {
    const r = await rpc<Record<string, never>, { dashboard: Dashboard }>("GetDashboard", {});
    return r.dashboard;
  },
  listJobs: async (status = "") => {
    const r = await rpc<{ status: string }, { jobs: Job[] }>("ListJobs", { status });
    return { jobs: r.jobs || [] };
  },
  createJob: async (title: string, description: string, location: string) => {
    const r = await rpc<object, { job: Job }>(
      "CreateJob",
      { title, description, location, status: "open" },
      { idempotencyKey: newIdempotencyKey("create_job") },
    );
    return r.job;
  },
  publishJob: async (id: string) => {
    const r = await rpc<{ id: string }, { job: Job }>(
      "PublishJob",
      { id },
      { idempotencyKey: newIdempotencyKey("publish") },
    );
    return r.job;
  },
  unpublishJob: async (id: string) => {
    const r = await rpc<{ id: string }, { job: Job }>(
      "UnpublishJob",
      { id },
      { idempotencyKey: newIdempotencyKey("unpublish") },
    );
    return r.job;
  },
  closeJob: async (id: string) => {
    const r = await rpc<{ id: string }, { job: Job }>("CloseJob", { id });
    return r.job;
  },
  listApplications: async (jobId: string) => {
    const r = await rpc<{ job_id: string }, { applications: Application[] }>("ListApplications", {
      job_id: jobId,
    });
    return { applications: r.applications || [] };
  },
  createApplication: async (jobId: string, profileId: string, summary?: string) => {
    const r = await rpc<object, { application: Application }>(
      "CreateApplication",
      { job_id: jobId, profile_id: profileId, summary },
      { idempotencyKey: newIdempotencyKey("create_app") },
    );
    return r.application;
  },
  listTalent: async (jobId: string) => {
    const r = await rpc<{ job_id: string; limit: number }, { talent: TalentHit[] }>("ListTalent", {
      job_id: jobId,
      limit: 20,
    });
    return { talent: r.talent || [] };
  },
  addTalent: async (jobId: string, hit: TalentHit) => {
    const r = await rpc<object, { application: Application }>(
      "AddTalent",
      { job_id: jobId, hit },
      { idempotencyKey: newIdempotencyKey("add_talent") },
    );
    return r.application;
  },
  advance: async (appId: string, toStage: string) => {
    const r = await rpc<object, { application: Application }>(
      "AdvanceApplication",
      { id: appId, to_stage: toStage },
      { idempotencyKey: newIdempotencyKey("advance") },
    );
    return r.application;
  },
  hire: async (appId: string) => {
    return rpc<{ id: string }, { application: Application }>(
      "HireApplication",
      { id: appId },
      { idempotencyKey: newIdempotencyKey("hire") },
    );
  },
  screenSummary: async (appId: string) => {
    return rpc<{ application_id: string }, { summary: string }>("ScreenSummary", {
      application_id: appId,
    });
  },
  proposeInterview: async (appId: string, durationMin = 30) => {
    const r = await rpc<object, { interview: Interview }>(
      "ProposeInterview",
      { application_id: appId, duration_min: durationMin, type: "screen" },
      { idempotencyKey: newIdempotencyKey("propose_iv") },
    );
    return r.interview;
  },
  listInterviews: async (applicationId: string) => {
    const r = await rpc<{ application_id: string }, { interviews: Interview[] }>("ListInterviews", {
      application_id: applicationId,
    });
    return { interviews: r.interviews || [] };
  },
  listSlots: async (interviewId: string) => {
    const r = await rpc<{ interview_id: string }, { slots: Slot[] }>("ListInterviewSlots", {
      interview_id: interviewId,
    });
    return { slots: r.slots || [] };
  },
  bookInterview: async (interviewId: string, start: string, end: string) => {
    const r = await rpc<object, { interview: Interview }>(
      "BookInterview",
      { interview_id: interviewId, start, end },
      { idempotencyKey: newIdempotencyKey("book") },
    );
    return r.interview;
  },
  listMyApplications: async () => {
    const r = await rpc<Record<string, never>, { applications: Application[] }>(
      "ListMyApplications",
      {},
    );
    return { applications: r.applications || [] };
  },
  getAvailability: () =>
    rpc<Record<string, never>, Availability | { availability: null }>("GetMyAvailability", {}),
  setAvailability: (body: { timezone: string; rules: Availability["rules"] }) =>
    rpc<object, { availability: Availability }>("SetMyAvailability", body, {
      idempotencyKey: newIdempotencyKey("avail"),
    }).then((r) => r.availability),
  getICS: async (interviewId: string) => {
    const r = await rpc<{ interview_id: string }, { ics: string }>("GetInterviewICS", {
      interview_id: interviewId,
    });
    return r.ics;
  },
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
