import { useCallback, useEffect, useMemo, useState } from "react";
import {
  api,
  NEXT_STAGE,
  STAGE_LABEL,
  type Application,
  type Dashboard,
  type Interview,
  type Job,
  type Slot,
  type TalentHit,
} from "./api";
import { authMode, ensureAuthReady, login, logout, type AuthMode } from "./auth";

type Tab = "today" | "jobs" | "pipeline" | "candidate" | "more";

const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

export function App() {
  const [tab, setTab] = useState<Tab>("today");
  const [dash, setDash] = useState<Dashboard | null>(null);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [selectedJob, setSelectedJob] = useState("");
  const [apps, setApps] = useState<Application[]>([]);
  const [myApps, setMyApps] = useState<Application[]>([]);
  const [talent, setTalent] = useState<TalentHit[]>([]);
  const [title, setTitle] = useState("");
  const [desc, setDesc] = useState("");
  const [location, setLocation] = useState("Remote");
  const [error, setError] = useState("");
  const [info, setInfo] = useState("");
  const [busy, setBusy] = useState(false);
  const [aiText, setAiText] = useState("");
  const [slots, setSlots] = useState<Slot[]>([]);
  const [bookingIv, setBookingIv] = useState("");
  const [candSlots, setCandSlots] = useState<Slot[]>([]);
  const [candBookingIv, setCandBookingIv] = useState("");
  const [tz, setTz] = useState("Africa/Nairobi");
  const [mode, setMode] = useState<AuthMode>(authMode());
  const [signedIn, setSignedIn] = useState(false);
  const [authReady, setAuthReady] = useState(false);

  const flash = (msg: string) => {
    setInfo(msg);
    setTimeout(() => setInfo(""), 3500);
  };

  const refreshDash = useCallback(async () => {
    try {
      setDash(await api.dashboard());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const refreshJobs = useCallback(async () => {
    try {
      const res = await api.listJobs();
      setJobs(res.jobs || []);
      if (!selectedJob && res.jobs?.[0]) setSelectedJob(res.jobs[0].id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [selectedJob]);

  const refreshApps = useCallback(async (jobId: string) => {
    if (!jobId) {
      setApps([]);
      return;
    }
    try {
      const res = await api.listApplications(jobId);
      setApps(res.applications || []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const refreshMyApps = useCallback(async () => {
    try {
      const res = await api.listMyApplications();
      setMyApps(res.applications || []);
    } catch {
      setMyApps([]);
    }
  }, []);

  useEffect(() => {
    void (async () => {
      setError("");
      const auth = await ensureAuthReady();
      setMode(auth.mode);
      setSignedIn(auth.signedIn);
      setAuthReady(true);
      if (!auth.signedIn && auth.mode === "oidc") return;
      if (!auth.signedIn && auth.mode === "none") {
        setError("Sign in required. Configure OIDC (VITE_OIDC_*) or local VITE_ATS_DEV_HEADERS=true.");
        return;
      }
      await refreshDash();
      await refreshJobs();
      await refreshMyApps();
    })();
  }, [refreshDash, refreshJobs, refreshMyApps]);

  useEffect(() => {
    if (!signedIn && mode !== "dev") return;
    void refreshApps(selectedJob);
    if (selectedJob) {
      void api
        .listTalent(selectedJob)
        .then((r) => setTalent(r.talent || []))
        .catch(() => setTalent([]));
    }
  }, [selectedJob, refreshApps, signedIn, mode]);

  const selectedJobObj = useMemo(
    () => jobs.find((j) => j.id === selectedJob),
    [jobs, selectedJob],
  );

  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  if (!authReady) {
    return (
      <div className="app">
        <header className="header">
          <h1>Stawi ATS</h1>
          <p className="muted">Loading session…</p>
        </header>
      </div>
    );
  }

  if (!signedIn && mode === "oidc") {
    return (
      <div className="app">
        <header className="header">
          <h1>Stawi ATS</h1>
          <p className="muted">Sign in with your Stawi account to hire or book interviews.</p>
        </header>
        <main className="main">
          {error ? <p className="error">{error}</p> : null}
          <button
            className="btn"
            disabled={busy}
            onClick={() =>
              void run(async () => {
                await login();
              })
            }
          >
            Sign in with Stawi
          </button>
        </main>
      </div>
    );
  }

  return (
    <div className="app">
      <header className="header">
        <h1>Stawi ATS</h1>
        <p className="muted">Hire in minutes · AI + talent + interviews</p>
      </header>
      <main className="main">
        {error ? <p className="error">{error}</p> : null}
        {info ? <p className="ok">{info}</p> : null}

        {tab === "today" && (
          <section>
            <div className="stats">
              <div className="stat">
                <strong>{dash?.open_jobs ?? "—"}</strong>
                <span>Open jobs</span>
              </div>
              <div className="stat">
                <strong>{dash?.active_applications ?? "—"}</strong>
                <span>In pipeline</span>
              </div>
              <div className="stat">
                <strong>{dash?.interviews_this_week ?? "—"}</strong>
                <span>Interviews</span>
              </div>
            </div>
            {dash?.needs_attention?.length ? (
              <div className="card warn">
                <h3>Next steps</h3>
                <ul>
                  {dash.needs_attention.map((n) => (
                    <li key={n}>{n}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            <h2 className="section-title">Upcoming interviews</h2>
            {(dash?.upcoming_interviews || []).length === 0 ? (
              <p className="muted">None this week. Open Pipeline → Schedule on a candidate.</p>
            ) : (
              dash!.upcoming_interviews.map((iv) => (
                <div key={iv.id} className="card">
                  <h3>{iv.job_title || "Interview"}</h3>
                  <p className="muted">
                    {iv.candidate_profile_id} ·{" "}
                    {iv.slot_start ? new Date(iv.slot_start).toLocaleString() : "—"}
                  </p>
                  <button
                    type="button"
                    className="link"
                    style={{ background: "none", border: 0, cursor: "pointer", padding: 0 }}
                    onClick={() =>
                      void (async () => {
                        try {
                          const ics = await api.getICS(iv.id);
                          const blob = new Blob([ics], { type: "text/calendar" });
                          const url = URL.createObjectURL(blob);
                          const a = document.createElement("a");
                          a.href = url;
                          a.download = "interview.ics";
                          a.click();
                          URL.revokeObjectURL(url);
                        } catch (e) {
                          setError(e instanceof Error ? e.message : String(e));
                        }
                      })()
                    }
                  >
                    Download ICS
                  </button>
                </div>
              ))
            )}
          </section>
        )}

        {tab === "jobs" && (
          <section>
            <label>
              Title
              <input
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder="Senior Backend Engineer"
              />
            </label>
            <label>
              Location
              <input value={location} onChange={(e) => setLocation(e.target.value)} />
            </label>
            <label>
              Description
              <textarea
                rows={3}
                value={desc}
                onChange={(e) => setDesc(e.target.value)}
                placeholder="What you’ll build…"
              />
            </label>
            <button
              className="btn"
              disabled={busy || !title.trim()}
              onClick={() =>
                void run(async () => {
                  await api.createJob(title.trim(), desc, location);
                  setTitle("");
                  setDesc("");
                  await refreshJobs();
                  await refreshDash();
                  flash("Job created");
                  setTab("pipeline");
                })
              }
            >
              Create open job
            </button>
            <div style={{ marginTop: "1rem" }}>
              {jobs.map((j) => (
                <div key={j.id} className="card">
                  <h3>{j.title}</h3>
                  <p className="muted">
                    {j.status} · {j.visibility}
                    {j.location ? ` · ${j.location}` : ""}
                  </p>
                  <div className="row">
                    <button
                      type="button"
                      className="btn sm"
                      onClick={() => {
                        setSelectedJob(j.id);
                        setTab("pipeline");
                      }}
                    >
                      Pipeline
                    </button>
                    {j.visibility === "private" ? (
                      <button
                        type="button"
                        className="btn sm secondary"
                        disabled={busy}
                        onClick={() =>
                          void run(async () => {
                            await api.publishJob(j.id);
                            await refreshJobs();
                            flash("Published to Stawi board projection");
                          })
                        }
                      >
                        Publish
                      </button>
                    ) : (
                      <button
                        type="button"
                        className="btn sm secondary"
                        disabled={busy}
                        onClick={() =>
                          void run(async () => {
                            await api.unpublishJob(j.id);
                            await refreshJobs();
                            flash("Unpublished");
                          })
                        }
                      >
                        Unpublish
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}

        {tab === "pipeline" && (
          <section>
            <label>
              Job
              <select value={selectedJob} onChange={(e) => setSelectedJob(e.target.value)}>
                {jobs.map((j) => (
                  <option key={j.id} value={j.id}>
                    {j.title}
                  </option>
                ))}
              </select>
            </label>
            {selectedJobObj ? (
              <p className="muted" style={{ marginTop: "0.35rem" }}>
                {selectedJobObj.description?.slice(0, 140) || "No description"}
              </p>
            ) : null}

            <h2 className="section-title">Stawi talent</h2>
            <p className="muted">Ranked shortlist from live candidate profiles when available.</p>
            {talent.length === 0 ? (
              <p className="muted">No talent hits yet — add by profile_id or connect matching DB.</p>
            ) : null}
            {talent.slice(0, 5).map((t) => (
              <div key={t.profile_id} className="card">
                <h3>{t.profile_id.replace(/^prof_/, "").replace(/_/g, " ")}</h3>
                <p className="muted">
                  score {(t.score ?? 0).toFixed(2)} · {t.summary}
                </p>
                <button
                  className="btn sm"
                  disabled={busy}
                  onClick={() =>
                    void run(async () => {
                      await api.addTalent(selectedJob, t);
                      await refreshApps(selectedJob);
                      await refreshDash();
                      flash("Added to pipeline");
                    })
                  }
                >
                  Add to pipeline
                </button>
              </div>
            ))}

            <h2 className="section-title">Pipeline</h2>
            <button
              className="btn secondary"
              disabled={busy || !selectedJob}
              onClick={() =>
                void run(async () => {
                  const pid = prompt("Candidate profile_id?");
                  if (!pid) return;
                  await api.createApplication(selectedJob, pid);
                  await refreshApps(selectedJob);
                  await refreshDash();
                })
              }
            >
              Add by profile_id
            </button>
            {apps.map((a) => (
              <div key={a.id} className="card">
                <div className="row between">
                  <h3>{a.profile_id}</h3>
                  <span className="badge">{STAGE_LABEL[a.stage] || a.stage}</span>
                </div>
                <p className="muted">
                  {a.source} · {a.status}
                  {a.summary ? ` · ${a.summary.slice(0, 80)}` : ""}
                </p>
                <div className="row wrap">
                  {a.status === "active" && NEXT_STAGE[a.stage] ? (
                    <button
                      className="btn sm"
                      disabled={busy}
                      onClick={() =>
                        void run(async () => {
                          if (NEXT_STAGE[a.stage] === "hired") {
                            await api.hire(a.id);
                          } else {
                            await api.advance(a.id, NEXT_STAGE[a.stage]);
                          }
                          await refreshApps(selectedJob);
                          await refreshDash();
                          flash(`Moved to ${STAGE_LABEL[NEXT_STAGE[a.stage]] || NEXT_STAGE[a.stage]}`);
                        })
                      }
                    >
                      → {STAGE_LABEL[NEXT_STAGE[a.stage]] || NEXT_STAGE[a.stage]}
                    </button>
                  ) : null}
                  <button
                    className="btn sm secondary"
                    disabled={busy}
                    onClick={() =>
                      void run(async () => {
                        const r = await api.screenSummary(a.id);
                        setAiText(r.summary);
                        flash("AI screen ready");
                      })
                    }
                  >
                    AI screen
                  </button>
                  <button
                    className="btn sm secondary"
                    disabled={busy}
                    onClick={() =>
                      void run(async () => {
                        const iv = await api.proposeInterview(a.id, 30);
                        const s = await api.listSlots(iv.id);
                        setBookingIv(iv.id);
                        setSlots(s.slots || []);
                        if (!(s.slots || []).length) {
                          setError("No free slots — set availability under More");
                        } else {
                          flash("Pick a slot below");
                        }
                      })
                    }
                  >
                    Schedule
                  </button>
                  {a.status === "active" && a.stage !== "hired" ? (
                    <button
                      className="btn sm danger"
                      disabled={busy}
                      onClick={() =>
                        void run(async () => {
                          await api.advance(a.id, "rejected");
                          await refreshApps(selectedJob);
                          await refreshDash();
                        })
                      }
                    >
                      Reject
                    </button>
                  ) : null}
                </div>
              </div>
            ))}

            {bookingIv && slots.length > 0 ? (
              <div className="card highlight">
                <h3>Pick interview slot</h3>
                <div className="slot-list">
                  {slots.slice(0, 12).map((s) => (
                    <button
                      key={s.start}
                      type="button"
                      className="slot"
                      disabled={busy}
                      onClick={() =>
                        void run(async () => {
                          await api.bookInterview(bookingIv, s.start, s.end);
                          setSlots([]);
                          setBookingIv("");
                          await refreshDash();
                          flash("Interview booked — invite enqueued");
                          setTab("today");
                        })
                      }
                    >
                      {new Date(s.start).toLocaleString(undefined, {
                        weekday: "short",
                        month: "short",
                        day: "numeric",
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </button>
                  ))}
                </div>
              </div>
            ) : null}

            {aiText ? (
              <div className="card">
                <h3>AI screen summary</h3>
                <pre className="pre">{aiText}</pre>
                <button type="button" className="btn sm secondary" onClick={() => setAiText("")}>
                  Dismiss
                </button>
              </div>
            ) : null}
          </section>
        )}

        {tab === "candidate" && (
          <section>
            <h2 className="section-title">My applications</h2>
            <p className="muted">Book proposed interviews for roles you are in.</p>
            <button
              className="btn secondary"
              disabled={busy}
              onClick={() => void run(async () => refreshMyApps())}
            >
              Refresh
            </button>
            {myApps.length === 0 ? (
              <p className="muted" style={{ marginTop: "0.75rem" }}>
                No applications on your profile yet.
              </p>
            ) : null}
            {myApps.map((a) => (
              <CandidateAppCard
                key={a.id}
                app={a}
                busy={busy}
                onBook={(ivId, s) =>
                  void run(async () => {
                    setCandBookingIv(ivId);
                    setCandSlots(s);
                    if (!s.length) setError("No free slots from the panel yet");
                    else flash("Pick a slot");
                  })
                }
                onError={(msg) => setError(msg)}
              />
            ))}
            {candBookingIv && candSlots.length > 0 ? (
              <div className="card highlight">
                <h3>Book your interview</h3>
                <div className="slot-list">
                  {candSlots.slice(0, 12).map((s) => (
                    <button
                      key={s.start}
                      type="button"
                      className="slot"
                      disabled={busy}
                      onClick={() =>
                        void run(async () => {
                          await api.bookInterview(candBookingIv, s.start, s.end);
                          setCandSlots([]);
                          setCandBookingIv("");
                          await refreshMyApps();
                          flash("You are booked — check email/ICS");
                        })
                      }
                    >
                      {new Date(s.start).toLocaleString(undefined, {
                        weekday: "short",
                        month: "short",
                        day: "numeric",
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </button>
                  ))}
                </div>
              </div>
            ) : null}
          </section>
        )}

        {tab === "more" && (
          <section>
            <h2 className="section-title">Interview availability</h2>
            <p className="muted">Weekday windows in your timezone. Candidates book into free slots.</p>
            <label>
              Timezone
              <input value={tz} onChange={(e) => setTz(e.target.value)} />
            </label>
            <button
              className="btn"
              disabled={busy}
              onClick={() =>
                void run(async () => {
                  const rules = [1, 2, 3, 4, 5].flatMap((weekday) => [
                    { weekday, start: "09:00", end: "12:00" },
                    { weekday, start: "14:00", end: "17:00" },
                  ]);
                  await api.setAvailability({ timezone: tz, rules });
                  await refreshDash();
                  flash(`Saved Mon–Fri availability (${WEEKDAYS.join("/")})`);
                })
              }
            >
              Set Mon–Fri 09–12 & 14–17
            </button>
            <h2 className="section-title">Account</h2>
            <p className="muted">
              Auth mode: <strong>{mode}</strong>
              {mode === "dev" ? " (X-Profile / tenant / partition headers)" : ""}
            </p>
            {mode === "oidc" ? (
              <button
                className="btn secondary"
                disabled={busy}
                onClick={() =>
                  void run(async () => {
                    await logout();
                    setSignedIn(false);
                  })
                }
              >
                Sign out
              </button>
            ) : null}
          </section>
        )}
      </main>
      <nav className="nav">
        {(
          [
            ["today", "Today"],
            ["jobs", "Jobs"],
            ["pipeline", "Pipeline"],
            ["candidate", "My apps"],
            ["more", "More"],
          ] as const
        ).map(([id, label]) => (
          <button
            key={id}
            type="button"
            className={tab === id ? "active" : ""}
            onClick={() => setTab(id)}
          >
            {label}
          </button>
        ))}
      </nav>
    </div>
  );
}

function CandidateAppCard({
  app,
  busy,
  onBook,
  onError,
}: {
  app: Application;
  busy: boolean;
  onBook: (interviewId: string, slots: Slot[]) => void;
  onError: (msg: string) => void;
}) {
  const [interviews, setInterviews] = useState<Interview[]>([]);
  useEffect(() => {
    void api
      .listInterviews(app.id)
      .then((r) => setInterviews(r.interviews || []))
      .catch(() => setInterviews([]));
  }, [app.id]);

  const proposed = interviews.filter((iv) => iv.status === "proposed");
  const scheduled = interviews.filter((iv) => iv.status === "scheduled");

  return (
    <div className="card" style={{ marginTop: "0.75rem" }}>
      <h3>{app.job_title || app.job_id}</h3>
      <p className="muted">
        {STAGE_LABEL[app.stage] || app.stage} · {app.status}
      </p>
      {scheduled.map((iv) => (
        <p key={iv.id} className="muted">
          Booked {iv.slot_start ? new Date(iv.slot_start).toLocaleString() : ""}
        </p>
      ))}
      {proposed.map((iv) => (
        <button
          key={iv.id}
          className="btn sm"
          disabled={busy}
          onClick={() =>
            void (async () => {
              try {
                const s = await api.listSlots(iv.id);
                onBook(iv.id, s.slots || []);
              } catch (e) {
                onError(e instanceof Error ? e.message : String(e));
              }
            })()
          }
        >
          Choose interview slot
        </button>
      ))}
      {proposed.length === 0 && scheduled.length === 0 ? (
        <p className="muted">No interview proposed yet.</p>
      ) : null}
    </div>
  );
}
