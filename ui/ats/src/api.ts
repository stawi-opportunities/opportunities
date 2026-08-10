/** Thin OpenAPI-style client. Dev headers until SPA identity login is wired. */

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
    const text = await res.text();
    throw new Error(`${res.status}: ${text}`);
  }
  return res.json() as Promise<T>;
}

export type Job = {
  ID: string;
  Title: string;
  Description: string;
  Status: string;
  Visibility: string;
};

export type Application = {
  ID: string;
  JobID: string;
  ProfileID: string;
  Stage: string;
  Status: string;
  Source: string;
};

export const api = {
  listJobs: () => req<{ jobs: Job[] }>("/v1/jobs"),
  createJob: (title: string, description: string) =>
    req<Job>("/v1/jobs", {
      method: "POST",
      body: JSON.stringify({ title, description, status: "open" }),
    }),
  listApplications: (jobId: string) =>
    req<{ applications: Application[] }>(`/v1/jobs/${jobId}/applications`),
  createApplication: (jobId: string, profileId: string) =>
    req<Application>(`/v1/jobs/${jobId}/applications`, {
      method: "POST",
      body: JSON.stringify({ profile_id: profileId }),
    }),
  advance: (appId: string, toStage: string) =>
    req<Application>(`/v1/applications/${appId}/advance`, {
      method: "POST",
      body: JSON.stringify({ to_stage: toStage }),
    }),
};
