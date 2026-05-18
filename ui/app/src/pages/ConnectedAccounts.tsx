// "Connected Accounts" — per-source onboarding for the Stawi
// AutoApply extension. Three sub-states the user can land in:
//
//   - No extension installed → "Install extension" prompt + pairing code
//   - Extension installed but no session for a source → instructions
//   - Source connected → green check + "Disconnect"
//
// The matching service treats `/sources/auth-manifest` as the canonical
// list of supported sources, so adding a new YAML there makes a new
// card appear here without any UI change.

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
  const { state } = useAuth();
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
    if (!confirm("Disconnect the extension? You'll need to pair it again to capture new sessions.")) {
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
    if (!confirm(`Disconnect ${sourceType}? Stawi will stop auto-applying to this source until you reconnect.`)) {
      return;
    }
    try {
      await revokeSession(sourceType);
      await refresh();
    } catch (err) {
      setError(toMessage(err));
    }
  }

  if (state !== "authenticated") {
    return <p>Please sign in to manage your connected accounts.</p>;
  }

  return (
    <section className="connected-accounts">
      <header>
        <h1>Connected accounts</h1>
        <p className="muted">
          Stawi can submit applications on your behalf to job boards you connect.
          Install the Stawi extension, sign in to each board yourself, and click
          <strong> Connect this account</strong> in the extension popup.
        </p>
      </header>

      <div className="pairing">
        <h2>Browser extension</h2>
        {!pairingCode ? (
          <button onClick={onCreatePairing}>Pair extension</button>
        ) : (
          <div className="pair-code">
            <p>
              Type this code into the Stawi extension popup. It expires in
              5 minutes.
            </p>
            <code>{pairingCode}</code>
            <button onClick={() => setPairingCode(null)}>Dismiss</button>
            <button onClick={onRevokeExtension} className="danger">
              Disconnect all extensions
            </button>
          </div>
        )}
      </div>

      {error && <p className="error">{error}</p>}
      {loading && !accounts && <p>Loading…</p>}

      <ul className="sources">
        {(accounts ?? []).map((a) => (
          <SourceCard key={a.source_type} acct={a} onRevoke={onRevokeSession} />
        ))}
        {accounts && accounts.length === 0 && (
          <li className="muted">No sources available yet.</li>
        )}
      </ul>
    </section>
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
    <li className="source">
      <div className="source-head">
        <h3>{acct.display_name}</h3>
        <StatusBadge status={status} />
      </div>
      <details>
        <summary>How to connect</summary>
        <div
          className="instructions"
          // instructions_md is plain markdown text the matching
          // service serves verbatim from the YAML manifest. Render as
          // pre-wrap so headings + steps remain readable without a
          // markdown engine in the bundle.
          style={{ whiteSpace: "pre-wrap" }}
        >
          {acct.instructions_md}
        </div>
      </details>
      <div className="actions">
        <a href={acct.login_url} target="_blank" rel="noopener noreferrer">
          Open login page ↗
        </a>
        {status === "connected" && (
          <button className="danger" onClick={() => onRevoke(acct.source_type)}>
            Disconnect
          </button>
        )}
        {status === "expired" && (
          <button className="danger" onClick={() => onRevoke(acct.source_type)}>
            Clear and reconnect
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
  return <span className={`status status-${status}`}>{label}</span>;
}

function toMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}
