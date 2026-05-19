// /connected/ — manages the candidate's connected job-board accounts.
//
// Each card maps 1:1 to a manifest in definitions/source-auth/*.yaml.
// Three sub-states the user can land in:
//
//   - No extension installed (or paired) → "Pair extension" + 6-char code
//   - Extension paired but no session for a source → instructions card
//   - Source connected → green "Connected ✓" + "Disconnect"
//
// The matching service treats /sources/auth-manifest as the canonical
// list of supported sources, so adding a new YAML makes a new card
// appear here without any UI change.

import { useEffect, useState } from "react";
import { useAuth } from "@/providers/AuthProvider";
import {
  fetchConnectedAccounts,
  createPairing,
  revokeExtension,
  revokeSession,
  type SourceAuthManifest,
  type ConnectedSession,
} from "@/api/sessions";

type Account = SourceAuthManifest & { session?: ConnectedSession };

export default function ConnectedAccounts() {
  const { state, login } = useAuth();
  const [accounts, setAccounts] = useState<Account[] | null>(null);
  const [pairingCode, setPairingCode] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function refresh() {
    setLoading(true);
    setError(null);
    try {
      const data = await fetchConnectedAccounts();
      setAccounts(data);
    } catch (err) {
      setError(toMessage(err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (state === "authenticated") {
      void refresh();
    }
  }, [state]);

  async function onCreatePairing() {
    setError(null);
    try {
      const resp = await createPairing();
      setPairingCode(resp.code);
    } catch (err) {
      setError(toMessage(err));
    }
  }

  async function onRevokeExtension() {
    if (
      !confirm(
        "Disconnect the extension? You'll need to pair it again to capture new sessions.",
      )
    ) {
      return;
    }
    try {
      await revokeExtension();
      setPairingCode(null);
      await refresh();
    } catch (err) {
      setError(toMessage(err));
    }
  }

  async function onRevokeSession(sourceType: string) {
    if (
      !confirm(
        `Disconnect ${sourceType}? Stawi will stop auto-applying to this source until you reconnect.`,
      )
    ) {
      return;
    }
    try {
      await revokeSession(sourceType);
      await refresh();
    } catch (err) {
      setError(toMessage(err));
    }
  }

  if (state === "unauthenticated") {
    return (
      <div className="mx-auto max-w-3xl px-4 py-12 sm:px-6 lg:px-8">
        <h1 className="text-2xl font-bold text-gray-900">Connected accounts</h1>
        <p className="mt-2 text-gray-600">
          Sign in to manage the job boards Stawi can auto-apply to.
        </p>
        <button
          onClick={() => void login()}
          className="mt-6 inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
        >
          Sign in
        </button>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-8 sm:px-6 lg:px-8">
      <header>
        <h1 className="text-2xl font-bold text-gray-900">Connected accounts</h1>
        <p className="mt-2 text-sm text-gray-600">
          Stawi can submit applications on your behalf to job boards you connect.
          Install the Stawi extension, sign in to each board yourself, then click{" "}
          <strong>Connect this account</strong> in the extension popup.
        </p>
      </header>

      <section className="mt-8 rounded-lg border border-gray-200 p-4">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-gray-500">
          Browser extension
        </h2>
        {!pairingCode ? (
          <button
            onClick={onCreatePairing}
            className="mt-3 inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
          >
            Pair extension
          </button>
        ) : (
          <div className="mt-3 space-y-3">
            <p className="text-sm text-gray-600">
              Type this code into the Stawi extension popup. It expires in 5 minutes.
            </p>
            <code className="inline-block rounded-md bg-gray-100 px-4 py-2 font-mono text-2xl tracking-[0.2em]">
              {pairingCode}
            </code>
            <div className="flex gap-3">
              <button
                onClick={() => setPairingCode(null)}
                className="rounded-md border border-gray-300 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-50"
              >
                Dismiss
              </button>
              <button
                onClick={onRevokeExtension}
                className="rounded-md bg-red-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-700"
              >
                Disconnect all extensions
              </button>
            </div>
          </div>
        )}
      </section>

      {error && (
        <p className="mt-4 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">
          {error}
        </p>
      )}
      {loading && !accounts && (
        <p className="mt-6 text-sm text-gray-500">Loading sources…</p>
      )}

      <ul className="mt-6 space-y-3">
        {(accounts ?? []).map((a) => (
          <SourceCard
            key={a.source_type}
            acct={a}
            onRevoke={onRevokeSession}
          />
        ))}
        {accounts && accounts.length === 0 && (
          <li className="rounded-md border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500">
            No sources available yet.
          </li>
        )}
      </ul>
    </div>
  );
}

function SourceCard({
  acct,
  onRevoke,
}: {
  acct: Account;
  onRevoke: (sourceType: string) => void;
}) {
  const status = acct.session?.status ?? "not_connected";
  return (
    <li className="rounded-lg border border-gray-200 p-4">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-base font-semibold text-gray-900">
          {acct.display_name}
        </h3>
        <StatusBadge status={status} />
      </div>
      <details className="mt-3">
        <summary className="cursor-pointer text-sm text-gray-600 hover:text-gray-900">
          How to connect
        </summary>
        <pre className="mt-2 whitespace-pre-wrap rounded-md bg-gray-50 p-3 text-xs leading-relaxed text-gray-700">
          {acct.instructions_md}
        </pre>
      </details>
      <div className="mt-3 flex items-center gap-4">
        <a
          href={acct.login_url}
          target="_blank"
          rel="noopener noreferrer"
          className="text-sm font-medium text-navy-900 hover:underline"
        >
          Open login page ↗
        </a>
        {(status === "connected" || status === "expired") && (
          <button
            onClick={() => onRevoke(acct.source_type)}
            className="rounded-md bg-red-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-red-700"
          >
            {status === "connected" ? "Disconnect" : "Clear and reconnect"}
          </button>
        )}
      </div>
    </li>
  );
}

function StatusBadge({ status }: { status: string }) {
  const label =
    status === "connected"
      ? "Connected ✓"
      : status === "expired"
        ? "Reconnect needed"
        : status === "revoked"
          ? "Revoked"
          : "Not connected";
  const cls =
    status === "connected"
      ? "bg-emerald-100 text-emerald-800"
      : status === "expired"
        ? "bg-amber-100 text-amber-800"
        : "bg-red-100 text-red-800";
  return (
    <span
      className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${cls}`}
    >
      {label}
    </span>
  );
}

function toMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
