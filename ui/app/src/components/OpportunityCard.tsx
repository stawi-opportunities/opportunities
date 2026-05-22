import type { FeedItem } from "@/api/candidates";

// Snapshot is the rich opportunity payload (title, company, etc.) the
// dashboard fetches separately from the feed. For now it's nullable;
// the card renders a placeholder when missing so a slow snapshot
// fetch doesn't block the row from showing up.
export interface OpportunitySnapshot {
  title: string;
  company?: string;
  location?: string;
  posted_at?: string;
  salary_min?: number;
  salary_max?: number;
  currency?: string;
  kind?: string;
}

interface Props {
  item: FeedItem;
  snapshot: OpportunitySnapshot | null;
  onStar: (opportunityId: string) => void;
  onUnstar: (opportunityId: string) => void;
  onApply: (opportunityId: string) => void;
}

const STATUS_LABEL: Record<string, string> = {
  applied: "Applied",
  responded: "Responded",
  interview: "Interview scheduled",
  offer: "Offer received",
  rejected: "Rejected",
  hired: "Hired",
};

export function OpportunityCard({ item, snapshot, onStar, onUnstar, onApply }: Props) {
  const title = snapshot?.title ?? "Loading…";
  const company = snapshot?.company ?? "";
  const location = snapshot?.location ?? "";

  return (
    <li className="flex flex-col gap-3 rounded-lg border border-gray-200 bg-white p-4 sm:flex-row sm:items-start sm:gap-4">
      <div className="flex-1">
        <div className="flex items-start justify-between gap-2">
          <div>
            <h3 className="text-base font-semibold text-gray-900">{title}</h3>
            {(company || location) && (
              <p className="mt-0.5 text-sm text-gray-600">
                {company}
                {company && location && " · "}
                {location}
              </p>
            )}
          </div>
          {typeof item.score === "number" && item.score > 0 && (
            <span
              className="shrink-0 rounded-full bg-emerald-50 px-2.5 py-0.5 text-xs font-medium text-emerald-700"
              title="Match score"
            >
              {Math.round(item.score * 100)}% match
            </span>
          )}
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          {item.application ? (
            <span className="inline-flex items-center rounded-full bg-blue-50 px-2.5 py-0.5 text-xs font-medium text-blue-700">
              {STATUS_LABEL[item.application.status] ?? item.application.status}
            </span>
          ) : (
            <button
              type="button"
              onClick={() => onApply(item.opportunity_id)}
              className="rounded-md bg-navy-900 px-3 py-1.5 text-sm font-medium text-white hover:bg-navy-800"
            >
              Apply
            </button>
          )}
          {item.starred ? (
            <button
              type="button"
              onClick={() => onUnstar(item.opportunity_id)}
              aria-label="Remove from saved"
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-amber-700 hover:bg-amber-50"
            >
              ★ Saved
            </button>
          ) : (
            <button
              type="button"
              onClick={() => onStar(item.opportunity_id)}
              aria-label="Save opportunity"
              className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
            >
              ☆ Save
            </button>
          )}
        </div>
      </div>
    </li>
  );
}
