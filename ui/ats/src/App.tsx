import { useCallback, useEffect, useState } from "react";
import { api, type Application, type Job } from "./api";

type Tab = "jobs" | "pipeline" | "today" | "more";

export function App() {
  const [tab, setTab] = useState<Tab>("jobs");
  const [jobs, setJobs] = useState<Job[]>([]);
  const [selectedJob, setSelectedJob] = useState<string>("");
  const [apps, setApps] = useState<Application[]>([]);
  const [title, setTitle] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const refreshJobs = useCallback(async () => {
    setError("");
    try {
      const res = await api.listJobs();
      setJobs(res.jobs || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void refreshJobs();
  }, [refreshJobs]);

  useEffect(() => {
    if (!selectedJob) {
      setApps([]);
      return;
    }
    void (async () => {
      try {
        const res = await api.listApplications(selectedJob);
        setApps(res.applications || []);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    })();
  }, [selectedJob]);

  async function createJob() {
    if (!title.trim()) return;
    setBusy(true);
    setError("");
    try {
      await api.createJob(title.trim(), "");
      setTitle("");
      await refreshJobs();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function addCandidate() {
    if (!selectedJob) return;
    const profileId = prompt("Candidate profile_id?");
    if (!profileId) return;
    setBusy(true);
    try {
      await api.createApplication(selectedJob, profileId);
      const res = await api.listApplications(selectedJob);
      setApps(res.applications || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function advance(app: Application) {
    const next: Record<string, string> = {
      applied: "screen",
      screen: "interview",
      interview: "offer",
      offer: "hired",
    };
    const to = next[app.Stage];
    if (!to) return;
    setBusy(true);
    try {
      await api.advance(app.ID, to);
      if (selectedJob) {
        const res = await api.listApplications(selectedJob);
        setApps(res.applications || []);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="app">
      <header className="header">
        <h1>Stawi ATS</h1>
        <p className="muted">Mobile-first hiring · API-driven</p>
      </header>
      <main className="main">
        {error ? <p className="error">{error}</p> : null}

        {tab === "jobs" && (
          <>
            <label>
              New job title
              <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Senior engineer" />
            </label>
            <button className="btn" disabled={busy} onClick={() => void createJob()}>
              Create job
            </button>
            <div style={{ marginTop: "1rem" }}>
              {jobs.map((j) => (
                <button
                  key={j.ID}
                  type="button"
                  className="card"
                  style={{ width: "100%", textAlign: "left", cursor: "pointer" }}
                  onClick={() => {
                    setSelectedJob(j.ID);
                    setTab("pipeline");
                  }}
                >
                  <h3>{j.Title || "(untitled)"}</h3>
                  <p className="muted">
                    {j.Status} · {j.Visibility}
                  </p>
                </button>
              ))}
              {!jobs.length ? <p className="muted">No jobs yet. Create one above.</p> : null}
            </div>
          </>
        )}

        {tab === "pipeline" && (
          <>
            {!selectedJob ? (
              <p className="muted">Pick a job from Jobs.</p>
            ) : (
              <>
                <p className="muted">Job {selectedJob.slice(0, 8)}…</p>
                <button className="btn" disabled={busy} onClick={() => void addCandidate()}>
                  Add candidate (profile_id)
                </button>
                {apps.map((a) => (
                  <div key={a.ID} className="card">
                    <h3>{a.ProfileID}</h3>
                    <p className="muted">
                      {a.Stage} · {a.Source} · {a.Status}
                    </p>
                    {a.Status === "active" && a.Stage !== "hired" ? (
                      <button className="btn" disabled={busy} onClick={() => void advance(a)}>
                        Advance
                      </button>
                    ) : null}
                  </div>
                ))}
              </>
            )}
          </>
        )}

        {tab === "today" && <p className="muted">Interviews for today will list here (API: /v1/interviews).</p>}
        {tab === "more" && (
          <p className="muted">
            Availability, partition switch, and outcome billing live here. Identity login replaces dev headers for
            production.
          </p>
        )}
      </main>
      <nav className="nav">
        {(
          [
            ["jobs", "Jobs"],
            ["pipeline", "Pipeline"],
            ["today", "Today"],
            ["more", "More"],
          ] as const
        ).map(([id, label]) => (
          <button key={id} type="button" className={tab === id ? "active" : ""} onClick={() => setTab(id)}>
            {label}
          </button>
        ))}
      </nav>
    </div>
  );
}
