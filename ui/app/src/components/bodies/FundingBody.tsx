import {
  fundingFocusArea,
  type OpportunitySnapshot,
} from "@/types/snapshot";
import { fmtMoney } from "@/utils/format";

/**
 * FundingBody renders kind=funding attributes: focus_area,
 * organisation_eligibility, target_regions, plus the grant amount
 * range derived from the universal amount_min/amount_max. Deadline is
 * already rendered in the universal header upstream.
 */
export default function FundingBody({ snap }: { snap: OpportunitySnapshot }) {
  const focus = fundingFocusArea(snap);
  const orgEligibility = stringAttr(snap, "organisation_eligibility");
  const targetRegions = stringArrayAttr(snap, "target_regions");
  const grant = fmtMoney(snap.amount_min, snap.amount_max, snap.currency, "");

  const description = snap.description ?? "";

  return (
    <>
      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-gray-600">
        {focus && (
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs capitalize">
            {focus}
          </span>
        )}
        {grant && (
          <span className="font-medium text-emerald-700">Grant: {grant}</span>
        )}
      </div>

      <dl className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
        {orgEligibility && (
          <Field label="Organisation eligibility" value={orgEligibility} />
        )}
        {targetRegions.length > 0 && (
          <Field label="Target regions" value={targetRegions.join(", ")} />
        )}
      </dl>

      {description && (
        <section
          className="prose prose-slate mt-8 max-w-none whitespace-pre-line"
          aria-label="Funding description"
        >
          {description}
        </section>
      )}
    </>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs font-medium uppercase tracking-wide text-gray-500">{label}</dt>
      <dd className="mt-1 text-sm text-gray-900">{value}</dd>
    </div>
  );
}

function stringAttr(snap: OpportunitySnapshot, key: string): string | undefined {
  const v = snap.attributes?.[key];
  return typeof v === "string" ? v : undefined;
}

function stringArrayAttr(snap: OpportunitySnapshot, key: string): string[] {
  const v = snap.attributes?.[key];
  if (!Array.isArray(v)) return [];
  return v.filter((x): x is string => typeof x === "string");
}
