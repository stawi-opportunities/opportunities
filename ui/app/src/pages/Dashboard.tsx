import { useEffect, useRef } from "react";
import { mount as mountProfile, type MountHandle } from "@stawi/profile";
import { useAuth } from "@/providers/AuthProvider";
import { getConfig } from "@/utils/config";

/**
 * /dashboard/ — landing for signed-in users.
 *
 * The left rail mounts the full @stawi/profile widget (profile details,
 * contact methods, verifications, admin link, logout). The right rail
 * lists candidate-specific domain data (matches, saved jobs, applications)
 * — those lists hit api.stawi.org/profile.v1.ProfileService via the
 * candidatesAPIURL and will be filled in when the backend endpoints land.
 */
export default function Dashboard() {
  const { state } = useAuth();

  if (state === "initializing") return <Skeleton />;
  if (state !== "authenticated") return <SignedOut />;

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <h1 className="text-3xl font-bold text-gray-900">Your dashboard</h1>
      <div className="mt-8 grid gap-8 lg:grid-cols-[320px_1fr]">
        <aside>
          <ProfileMount />
        </aside>
        <section>
          <PlaceholderPanel title="Matches" message="Your AI matches will show up here." />
          <PlaceholderPanel title="Saved jobs" message="Jobs you've saved." />
          <PlaceholderPanel title="Applications" message="Jobs we've applied to on your behalf." />
        </section>
      </div>
    </div>
  );
}

/** Mounts the @stawi/profile widget inside its own shadow DOM. */
function ProfileMount() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const cfg = getConfig();
    const handle: MountHandle = mountProfile({
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
    return () => handle.unmount();
  }, []);
  return <div ref={hostRef} />;
}

function PlaceholderPanel({ title, message }: { title: string; message: string }) {
  return (
    <div className="mb-6 rounded-lg border border-gray-200 bg-white p-6">
      <h2 className="text-lg font-semibold text-gray-900">{title}</h2>
      <p className="mt-2 text-sm text-gray-500">{message}</p>
    </div>
  );
}

function SignedOut() {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">You're signed out</h1>
      <p className="mt-2 text-gray-600">Sign in from the top right to see your dashboard.</p>
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
