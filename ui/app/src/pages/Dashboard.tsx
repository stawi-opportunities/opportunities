import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { mount as mountProfile, type MountHandle } from "@stawi/profile";
import { useAuth } from "@/providers/AuthProvider";
import { getConfig } from "@/utils/config";
import { fetchMeSubscription } from "@/api/candidates";
import { planById, type PlanId } from "@/utils/plans";

/**
 * /dashboard/ — the working surface for signed-in candidates. The view
 * adapts to the active subscription tier:
 *
 *   free     — upgrade nudge + saved-jobs + browse shortcut
 *   starter  — match queue, weekly digest toggle, upgrade-to-pro nudge
 *   pro      — larger queue + cover-letter shortcuts
 *   managed  — agent card (name, email, direct line) replaces the queue UI
 *
 * Tier is read from GET /me/subscription; the call falls back to the free
 * view on 401/404 so the page is never broken for unauthenticated visitors
 * or while the endpoint is still stubbed on the backend.
 */
export default function Dashboard() {
  const { state, login } = useAuth();

  const subQ = useQuery({
    queryKey: ["me-subscription"],
    queryFn: fetchMeSubscription,
    enabled: state === "authenticated",
    staleTime: 60_000,
  });

  if (state === "initializing") return <Skeleton />;
  if (state !== "authenticated") return <SignedOut onSignIn={login} />;

  const sub = subQ.data;
  const plan = sub?.plan ?? "free";
  const planInfo = planById(plan);

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <DashboardHeader plan={plan} />

      <div className="mt-8 grid gap-8 lg:grid-cols-[320px_1fr]">
        <aside>
          <ProfileMount />
        </aside>
        <section className="space-y-6">
          {plan === "managed" && sub?.agent && (
            <AgentCard agent={sub.agent} />
          )}
          {plan === "free" && <UpgradeCta />}
          <MatchesPanel
            plan={plan}
            queued={sub?.queued_matches ?? 0}
            delivered={sub?.delivered_this_week ?? 0}
          />
          <SavedJobsPanel />
          {plan !== "managed" && <ApplicationsPanel plan={plan} />}
          <BillingPanel plan={plan} status={sub?.status ?? "none"} renewsAt={sub?.renews_at} />
          <PlanFeaturesNote plan={plan} features={planInfo.features.slice(0, 4)} />
        </section>
      </div>
    </div>
  );
}

function DashboardHeader({ plan }: { plan: PlanId }) {
  const planInfo = planById(plan);
  return (
    <header className="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-3xl font-bold text-gray-900">Your dashboard</h1>
        <p className="mt-1 flex items-center gap-2 text-gray-600">
          <span className="inline-flex items-center rounded-full bg-gray-100 px-2.5 py-0.5 text-xs font-medium text-gray-700">
            {planInfo.name} plan
          </span>
          <span>{planInfo.tagline}</span>
        </p>
      </div>
      <div className="flex items-center gap-3">
        <a
          href="/jobs/"
          className="text-sm font-medium text-gray-700 hover:text-navy-900"
        >
          Browse jobs
        </a>
        {plan !== "managed" && (
          <a
            href="/pricing/"
            className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
          >
            {plan === "free" ? "Upgrade" : "Change plan"}
          </a>
        )}
      </div>
    </header>
  );
}

function AgentCard({ agent }: { agent: { name: string; email: string } }) {
  return (
    <div className="rounded-lg border border-accent-200 bg-accent-50 p-6">
      <p className="text-xs font-semibold uppercase tracking-wide text-accent-700">
        Your agent
      </p>
      <h2 className="mt-2 text-xl font-bold text-gray-900">{agent.name}</h2>
      <p className="mt-1 text-sm text-gray-700">
        Your personal recruiter for the duration of your search. Reach out
        any time and expect a same-day response.
      </p>
      <div className="mt-4 flex flex-wrap gap-3">
        <a
          href={`mailto:${agent.email}`}
          className="inline-flex items-center rounded-md bg-accent-500 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-accent-600"
        >
          Email {agent.name.split(" ")[0]}
        </a>
        <a
          href="#schedule"
          className="inline-flex items-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          Schedule a 1:1
        </a>
      </div>
    </div>
  );
}

function UpgradeCta() {
  return (
    <div className="rounded-lg border border-navy-900 bg-navy-900 p-6 text-white">
      <p className="text-xs font-semibold uppercase tracking-wide text-accent-300">
        Unlock matching
      </p>
      <h2 className="mt-2 text-xl font-bold">
        Upgrade to get matches delivered to you
      </h2>
      <p className="mt-1 text-sm text-gray-300">
        Starter ($10/month) gives you 5 AI-matched roles every week based on
        your CV. Pro ($50) gives you 25/week plus cover-letter drafts.
        Managed ($200) assigns a real person to run your search.
      </p>
      <div className="mt-4 flex flex-wrap gap-3">
        <a
          href="/onboarding/?plan=starter"
          className="inline-flex items-center rounded-md bg-accent-500 px-4 py-2 text-sm font-semibold text-white shadow-sm hover:bg-accent-600"
        >
          Start Starter — $10/month
        </a>
        <a
          href="/pricing/"
          className="inline-flex items-center rounded-md border border-white/20 bg-white/10 px-4 py-2 text-sm font-medium text-white hover:bg-white/20"
        >
          Compare plans
        </a>
      </div>
    </div>
  );
}

function MatchesPanel({
  plan,
  queued,
  delivered,
}: {
  plan: PlanId;
  queued: number;
  delivered: number;
}) {
  if (plan === "free") return null;
  if (plan === "managed") {
    return (
      <Panel title="Matches">
        <p className="text-sm text-gray-600">
          Your agent hand-picks roles that pass their screen before they
          reach you. Expect curated matches in your inbox and a weekly
          summary on your 1:1 call.
        </p>
      </Panel>
    );
  }
  const planInfo = planById(plan);
  const cap = planInfo.matchesPerWeek ?? 0;
  return (
    <Panel title="Matches">
      <div className="grid grid-cols-2 gap-4">
        <div>
          <p className="text-xs font-medium uppercase tracking-wide text-gray-500">
            Delivered this week
          </p>
          <p className="mt-1 text-2xl font-bold text-gray-900">
            {delivered}
            <span className="text-sm font-normal text-gray-500"> / {cap}</span>
          </p>
        </div>
        <div>
          <p className="text-xs font-medium uppercase tracking-wide text-gray-500">
            In your queue
          </p>
          <p className="mt-1 text-2xl font-bold text-gray-900">{queued}</p>
        </div>
      </div>
      {plan === "starter" && (
        <div className="mt-4 rounded-md border border-gray-200 bg-gray-50 p-3 text-sm text-gray-700">
          Want 5× the matches and priority placement in the queue?{" "}
          <a href="/pricing/" className="font-medium text-accent-600 hover:text-accent-700">
            Upgrade to Pro →
          </a>
        </div>
      )}
      {delivered === 0 && (
        <p className="mt-4 text-sm text-gray-500">
          Your first matches will arrive within 24 hours of onboarding.
        </p>
      )}
    </Panel>
  );
}

function SavedJobsPanel() {
  return (
    <Panel title="Saved jobs">
      <p className="text-sm text-gray-600">
        Save any listing with the bookmark icon and it'll appear here.
      </p>
      <a
        href="/jobs/"
        className="mt-4 inline-block text-sm font-medium text-accent-600 hover:text-accent-700"
      >
        Browse jobs →
      </a>
    </Panel>
  );
}

function ApplicationsPanel({ plan }: { plan: PlanId }) {
  return (
    <Panel title="Applications">
      <p className="text-sm text-gray-600">
        {plan === "pro"
          ? "Pro includes cover-letter drafts for every match. Open a match to generate one."
          : "Every listing links to the employer's own application page — we'll track the ones you start from here once the employer-callback integrations ship."}
      </p>
    </Panel>
  );
}

function BillingPanel({
  plan,
  status,
  renewsAt,
}: {
  plan: PlanId;
  status: "none" | "active" | "past_due" | "cancelled";
  renewsAt?: string;
}) {
  if (plan === "free") {
    return (
      <Panel title="Billing">
        <p className="text-sm text-gray-600">
          You're on the Free plan — no card on file.
        </p>
      </Panel>
    );
  }
  const info = planById(plan);
  return (
    <Panel title="Billing">
      <p className="text-sm text-gray-700">
        <span className="font-medium">{info.name}</span> · ${info.price}/month ·{" "}
        <span
          className={
            status === "active"
              ? "text-emerald-700"
              : status === "past_due"
                ? "text-amber-700"
                : "text-gray-500"
          }
        >
          {status === "active"
            ? "Active"
            : status === "past_due"
              ? "Past due"
              : status === "cancelled"
                ? "Cancelled"
                : "Pending payment"}
        </span>
      </p>
      {renewsAt && status === "active" && (
        <p className="mt-1 text-xs text-gray-500">
          Renews on {new Date(renewsAt).toLocaleDateString()}
        </p>
      )}
      <a
        href="/pricing/"
        className="mt-3 inline-block text-sm font-medium text-accent-600 hover:text-accent-700"
      >
        Change plan or cancel →
      </a>
    </Panel>
  );
}

function PlanFeaturesNote({ plan, features }: { plan: PlanId; features: string[] }) {
  if (plan === "free") return null;
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-5">
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
        What's included
      </p>
      <ul className="mt-3 space-y-1.5 text-sm text-gray-700" role="list">
        {features.map((f) => (
          <li key={f} className="flex items-start gap-2">
            <svg
              className="mt-0.5 h-4 w-4 shrink-0 text-emerald-500"
              fill="currentColor"
              viewBox="0 0 20 20"
              aria-hidden="true"
            >
              <path
                fillRule="evenodd"
                d="M16.704 5.29a1 1 0 010 1.42l-8 8a1 1 0 01-1.42 0l-4-4a1 1 0 111.42-1.42L8 12.584l7.29-7.294a1 1 0 011.414 0z"
                clipRule="evenodd"
              />
            </svg>
            <span>{f}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-6">
      <h2 className="text-lg font-semibold text-gray-900">{title}</h2>
      <div className="mt-2">{children}</div>
    </div>
  );
}

function ProfileMount() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    let handle: MountHandle | null = null;
    try {
      const cfg = getConfig();
      handle = mountProfile({
        target: host,
        clientId: cfg.oidcClientID,
        installationId: cfg.oidcInstallationID,
        idpBaseUrl: cfg.oidcIssuer,
        apiBaseUrl: cfg.candidatesAPIURL,
        theme: "light",
        onLogout: () => {
          window.location.href = "/";
        },
      });
    } catch {
      // Widget failed to mount — skeleton stays visible; sign out from nav.
    }
    return () => handle?.unmount();
  }, []);
  return <div ref={hostRef} className="min-h-[320px]" />;
}

function SignedOut({ onSignIn }: { onSignIn: () => Promise<void> }) {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">Sign in to continue</h1>
      <p className="mt-2 text-gray-600">
        Your dashboard shows your plan, matches, and saved jobs.
      </p>
      <button
        type="button"
        onClick={() => void onSignIn()}
        className="mt-6 rounded-md bg-navy-900 px-5 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
      >
        Sign in
      </button>
    </div>
  );
}

function Skeleton() {
  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <div className="h-8 w-48 animate-pulse rounded bg-gray-100" />
      <div className="mt-8 grid gap-8 lg:grid-cols-[320px_1fr]">
        <div className="h-96 animate-pulse rounded-lg bg-gray-100" />
        <div className="h-96 animate-pulse rounded-lg bg-gray-100" />
      </div>
    </div>
  );
}
