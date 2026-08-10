/**
 * Connect-protocol client for ats.v1.AtsService (JSON).
 * Type-safe surface matches apps/ats/proto/ats/v1/ats.proto.
 */

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

const tenancyHeaders = (): HeadersInit => ({
  "Content-Type": "application/json",
  "Connect-Protocol-Version": "1",
  "X-Profile-ID": localStorage.getItem("ats_profile_id") || "dev-recruiter",
  "X-Tenant-ID": localStorage.getItem("ats_tenant_id") || "dev-tenant",
  "X-Partition-ID": localStorage.getItem("ats_partition_id") || "dev-partition",
});

async function rpc<TReq extends object, TRes>(method: string, body: TReq): Promise<TRes> {
  const res = await fetch(`${SERVICE}/${method}`, {
    method: "POST",
    headers: tenancyHeaders(),
    body: JSON.stringify(body ?? {}),
  });
  if (!res.ok) {
    let detail = await res.text();
    try {
      const j = JSON.parse(detail);
      detail = j.message || j.detail || j.title || detail;
    } catch {
      /* keep */
    }
    throw new Error(detail || `${res.status}`);
  }
  if (res.status === 204) return undefined as TRes;
  return res.json() as Promise<TRes>;
}

export const api = {
  dashboard: async () => {
    const r = await rpc<Record<string, never>, { dashboard: Dashboard }>("GetDashboard", {});
    return r.dashboard;
  },
  seed: () => rpc<Record<string, never>, { seeded: boolean }>("SeedDemo", {}),
  listJobs: async (status = "") => {
    const r = await rpc<{ status: string }, { jobs: Job[] }>("ListJobs", { status });
    return { jobs: r.jobs || [] };
  },
  createJob: async (title: string, description: string, location: string) => {
    const r = await rpc<object, { job: Job }>("CreateJob", {
      title,
      description,
      location,
      status: "open",
    });
    return r.job;
  },
  publishJob: async (id: string) => {
    const r = await rpc<{ id: string }, { job: Job }>("PublishJob", { id });
    return r.job;
  },
  unpublishJob: async (id: string) => {
    const r = await rpc<{ id: string }, { job: Job }>("UnpublishJob", { id });
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
    const r = await rpc<object, { application: Application }>("CreateApplication", {
      job_id: jobId,
      profile_id: profileId,
      summary,
    });
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
    const r = await rpc<object, { application: Application }>("AddTalent", {
      job_id: jobId,
      hit,
    });
    return r.application;
  },
  advance: async (appId: string, toStage: string) => {
    const r = await rpc<object, { application: Application }>("AdvanceApplication", {
      id: appId,
      to_stage: toStage,
    });
    return r.application;
  },
  hire: async (appId: string) => {
    return rpc<{ id: string }, { application: Application }>("HireApplication", { id: appId });
  },
  screenSummary: async (appId: string) => {
    return rpc<{ application_id: string }, { summary: string }>("ScreenSummary", {
      application_id: appId,
    });
  },
  proposeInterview: async (appId: string, durationMin = 30) => {
    const r = await rpc<object, { interview: Interview }>("ProposeInterview", {
      application_id: appId,
      duration_min: durationMin,
      type: "screen",
    });
    return r.interview;
  },
  listSlots: async (interviewId: string) => {
    const r = await rpc<{ interview_id: string }, { slots: Slot[] }>("ListInterviewSlots", {
      interview_id: interviewId,
    });
    return { slots: r.slots || [] };
  },
  bookInterview: async (interviewId: string, start: string, end: string) => {
    const r = await rpc<object, { interview: Interview }>("BookInterview", {
      interview_id: interviewId,
      start,
      end,
    });
    return r.interview;
  },
  getAvailability: () => rpc<Record<string, never>, Availability | { availability: null }>("GetMyAvailability", {}),
  setAvailability: (body: { timezone: string; rules: Availability["rules"] }) =>
    rpc<object, { availability: Availability }>("SetMyAvailability", body).then((r) => r.availability),
  icsUrl: (interviewId: string) => {
    // ICS via RPC response in production; for download we use GetInterviewICS then blob.
    return interviewId;
  },
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
