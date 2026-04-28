import { useState } from "react";
import { authRuntime } from "@/auth/runtime";

/**
 * FlagModal renders the "Flag this listing" dialog wired into a
 * per-kind body component. Auth is required server-side — the
 * authRuntime().fetch() path attaches the JWT automatically. On
 * success the modal switches to a confirmation state; on duplicate
 * (409) it surfaces the "you've already flagged this" message
 * inline.
 *
 * v1 wires only into JobBody — other kinds adopt the same pattern in
 * a follow-up by importing this component and dropping it into their
 * body markup. The component is self-contained: it does not subscribe
 * to any global state, and it renders nothing visible until the
 * trigger button is clicked.
 */

const REASONS = [
  { value: "scam", label: "Scam or phishing" },
  { value: "expired", label: "Expired or filled" },
  { value: "duplicate", label: "Duplicate listing" },
  { value: "spam", label: "Spam or low-quality" },
  { value: "other", label: "Other" },
] as const;

const MAX_DESCRIPTION = 1000;

type SubmitState =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "ok" }
  | { kind: "duplicate" }
  | { kind: "unauthorized" }
  | { kind: "error"; message: string };

export default function FlagModal({ slug }: { slug: string }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState<string>("scam");
  const [description, setDescription] = useState("");
  const [state, setState] = useState<SubmitState>({ kind: "idle" });

  function reset(): void {
    setReason("scam");
    setDescription("");
    setState({ kind: "idle" });
  }

  async function handleSubmit(e: Event): Promise<void> {
    e.preventDefault();
    if (state.kind === "submitting") return;
    setState({ kind: "submitting" });
    try {
      await authRuntime().fetch(`/opportunities/${encodeURIComponent(slug)}/flag`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ reason, description: description.trim() }),
      });
      setState({ kind: "ok" });
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      // The runtime surfaces non-2xx as throws — sniff the status from
      // the message for the duplicate / unauthorized special-cases.
      if (message.includes("409") || message.toLowerCase().includes("already_flagged")) {
        setState({ kind: "duplicate" });
      } else if (message.includes("401") || message.toLowerCase().includes("unauthorized")) {
        setState({ kind: "unauthorized" });
      } else {
        setState({ kind: "error", message });
      }
    }
  }

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => {
          reset();
          setOpen(true);
        }}
        className="mt-6 inline-flex items-center gap-1 text-sm text-gray-500 hover:text-red-700"
      >
        <span aria-hidden>⚠</span>
        Flag this listing
      </button>
    );
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="flag-modal-title"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) setOpen(false);
      }}
    >
      <div className="w-full max-w-md rounded-lg bg-white p-6 shadow-xl">
        <h3 id="flag-modal-title" className="text-lg font-semibold text-gray-900">
          Flag this listing
        </h3>

        {state.kind === "ok" ? (
          <div className="mt-4 space-y-3 text-sm text-gray-700">
            <p>
              Thanks — your report has been recorded and will be reviewed by
              our moderators.
            </p>
            <button
              type="button"
              onClick={() => setOpen(false)}
              className="rounded bg-navy-700 px-4 py-2 text-sm font-medium text-white"
            >
              Close
            </button>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="mt-4 space-y-4">
            <div>
              <label htmlFor="flag-reason" className="block text-sm font-medium text-gray-700">
                Reason
              </label>
              <select
                id="flag-reason"
                value={reason}
                onChange={(e) => setReason((e.target as HTMLSelectElement).value)}
                className="mt-1 block w-full rounded border border-gray-300 px-3 py-2 text-sm"
              >
                {REASONS.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label htmlFor="flag-description" className="block text-sm font-medium text-gray-700">
                Details (optional)
              </label>
              <textarea
                id="flag-description"
                value={description}
                onChange={(e) => setDescription((e.target as HTMLTextAreaElement).value)}
                maxLength={MAX_DESCRIPTION}
                rows={4}
                className="mt-1 block w-full rounded border border-gray-300 px-3 py-2 text-sm"
                placeholder="What looks wrong about this listing?"
              />
              <p className="mt-1 text-right text-xs text-gray-500">
                {description.length}/{MAX_DESCRIPTION}
              </p>
            </div>

            {state.kind === "duplicate" && (
              <p className="text-sm text-amber-700">
                You've already flagged this listing — thanks for keeping the
                site clean.
              </p>
            )}
            {state.kind === "unauthorized" && (
              <p className="text-sm text-red-700">
                You need to be signed in to flag a listing.
              </p>
            )}
            {state.kind === "error" && (
              <p className="text-sm text-red-700">
                Could not submit flag: {state.message}
              </p>
            )}

            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="rounded border border-gray-300 px-4 py-2 text-sm text-gray-700"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={state.kind === "submitting"}
                className="rounded bg-red-700 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
              >
                {state.kind === "submitting" ? "Submitting…" : "Submit flag"}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
