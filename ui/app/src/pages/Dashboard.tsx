import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { mount as mountProfile, type MountHandle } from "@stawi/profile";
import { useAuth } from "@/providers/AuthProvider";
import { getConfig } from "@/utils/config";
import { fetchMeSubscription } from "@/api/candidates";
import { normalizePlan, planById, type PlanId } from "@/utils/plans";

/**
 * /dashboard/ — the working surface for signed-in candidates.
 *
 * There is no free tier. Every dashboard visitor is in one of two
 * states:
 *
 *   1. No active subscription yet — profile exists but payment hasn't
 *      completed. We show a "Complete payment" nudge + access to their
 *      onboarded preferences.
 *   2. Active on starter / pro / managed — tier-specific surface:
 *        starter  → match queue, weekly digest, upgrade-to-pro nudge
 *        pro      → larger queue + cover-letter shortcuts
 *        managed  → agent card (name, email, direct line) + curated matches
 *
 * Tier comes from GET /me/subscription.
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
  const plan = normalizePlan(sub?.plan ?? null);
  const isActive = sub?.status === "active";

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <DashboardHeader plan={plan} active={isActive} />

      <div className="mt-8 grid gap-8 lg:grid-cols-[320px_1fr]">
        <aside>
          <ProfileMount />
        </aside>
        <section className="space-y-6">
          {plan === null || !isActive ? (
            <CompletePaymentPanel plan={plan} status={sub?.status ?? "none"} />
          ) : (
            <>
              {plan === "managed" && sub?.agent && (
                <AgentCard agent={sub.agent} />
              )}
              <MatchesPanel
                plan={plan}
                queued={sub?.queued_matches ?? 0}
                delivered={sub?.delivered_this_week ?? 0}
              />
            </>
          )}
          <SavedJobsPanel />
          {plan && plan !== "managed" && isActive && <ApplicationsPanel plan={plan} />}
          {plan && isActive && (
            <BillingPanel plan={plan} renewsAt={sub?.renews_at} />
          )}
        </section>
      </div>
    </div>
  );
}

function DashboardHeader({
  plan,
  active,
}: {
  plan: PlanId | null;
  active: boolean;
}) {
  const label = plan && active ? planById(plan).name : "Setup incomplete";
  const tagline =
    plan && active
      ? planById(plan).tagline
      : "Finish payment to unlock matching.";
  return (
    <header className="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-3xl font-bold text-gray-900">Your dashboard</h1>
        <p className="mt-1 flex items-center gap-2 text-gray-600">
          <span
            className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
              plan && active
                ? "bg-emerald-50 text-emerald-700"
                : "bg-amber-50 text-amber-700"
            }`}
          >
            {label}
          </span>
          <span>{tagline}</span>
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
            {active ? "Change plan" : "View plans"}
          </a>
        )}
      </div>
    </header>
  );
}

function CompletePaymentPanel({
  plan,
  status,
}: {
  plan: PlanId | null;
  status: string;
}) {
  const headline =
    status === "past_due"
      ? "Your last payment didn't go through"
      : status === "cancelled"
        ? "Your subscription is cancelled"
        : plan
          ? `Finish setting up your ${planById(plan).name} plan`
          : "Pick a plan to start matching";
  const body =
    status === "past_due"
      ? "Update your payment details to resume matching."
      : status === "cancelled"
        ? "Re-activate any time to start receiving matches again."
        : "We'll only run our matching engine on your CV once a plan is active. It takes two minutes.";
  return (
    <div className="rounded-lg border border-amber-300 bg-amber-50 p-6">
      <p className="text-xs font-semibold uppercase tracking-wide text-amber-700">
        Action needed
      </p>
      <h2 className="mt-2 text-xl font-bold text-gray-900">{headline}</h2>
      <p className="mt-1 text-sm text-gray-700">{body}</p>
      <div className="mt-4 flex flex-wrap gap-3">
        <a
          href="/pricing/"
          className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-semibold text-white hover:bg-navy-800"
        >
          {plan && status !== "cancelled" ? `Pay $${planById(plan).price}/mo` : "Choose a plan"}
        </a>
        <a
          href="/onboarding/"
          className="inline-flex items-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          Edit preferences
        </a>
      </div>
    </div>
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
          className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-semibold text-white hover:bg-navy-800"
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

function MatchesPanel({
  plan,
  queued,
  delivered,
}: {
  plan: PlanId;
  queued: number;
  delivered: number;
}) {
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
          Your first matches will arrive within 24 hours of payment.
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
  renewsAt,
}: {
  plan: PlanId;
  renewsAt?: string;
}) {
  const info = planById(plan);
  return (
    <Panel title="Billing">
      <p className="text-sm text-gray-700">
        <span className="font-medium">{info.name}</span> · ${info.price}/month ·{" "}
        <span className="text-emerald-700">Active</span>
      </p>
      {renewsAt && (
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
