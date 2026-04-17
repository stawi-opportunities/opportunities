import { useEffect, useRef } from "react";
import { mount as mountProfile, type MountHandle } from "@stawi/profile";
import { useAuth } from "@/providers/AuthProvider";
import { getConfig } from "@/utils/config";

/**
 * /dashboard/ — landing for signed-in candidates.
 *
 * The left rail mounts @stawi/profile (account details, verifications,
 * logout). The right rail hosts candidate-specific surfaces — matches,
 * saved jobs, applications — which render empty-state CTAs until the
 * matching backend endpoints land.
 */
export default function Dashboard() {
  const { state, login } = useAuth();

  if (state === "initializing") return <Skeleton />;
  if (state !== "authenticated") return <SignedOut onSignIn={login} />;

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-bold text-gray-900">Your dashboard</h1>
          <p className="mt-1 text-gray-600">
            Your profile, saved jobs, and match history live here.
          </p>
        </div>
        <a
          href="/jobs/"
          className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
        >
          Browse jobs
        </a>
      </header>
      <div className="mt-8 grid gap-8 lg:grid-cols-[320px_1fr]">
        <aside>
          <ProfileMount />
        </aside>
        <section className="space-y-6">
          <EmptyPanel
            title="Matches"
            body="We'll surface roles that match your target title, seniority, and regions within a few minutes of completing onboarding."
            cta={{ href: "/onboarding/", label: "Update preferences" }}
          />
          <EmptyPanel
            title="Saved jobs"
            body="Save a job from any listing to come back to it later."
            cta={{ href: "/jobs/", label: "Browse jobs" }}
          />
          <EmptyPanel
            title="Applications"
            body="Every role links to the employer's own application page. We'll track applications you start from here once the employer-callback integrations ship."
          />
        </section>
      </div>
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
      // Widget failed to mount — keep the skeleton visible; the user can
      // still sign out from the nav.
    }
    return () => handle?.unmount();
  }, []);
  return <div ref={hostRef} className="min-h-[320px]" />;
}

function EmptyPanel({
  title,
  body,
  cta,
}: {
  title: string;
  body: string;
  cta?: { href: string; label: string };
}) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-6">
      <h2 className="text-lg font-semibold text-gray-900">{title}</h2>
      <p className="mt-2 text-sm text-gray-600">{body}</p>
      {cta && (
        <a
          href={cta.href}
          className="mt-4 inline-block text-sm font-medium text-accent-600 hover:text-accent-700"
        >
          {cta.label} →
        </a>
      )}
    </div>
  );
}

function SignedOut({ onSignIn }: { onSignIn: () => Promise<void> }) {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">Sign in to continue</h1>
      <p className="mt-2 text-gray-600">
        Your dashboard shows your profile, matches, and saved jobs.
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
