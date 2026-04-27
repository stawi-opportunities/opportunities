import {
  tenderProcurementDomain,
  type OpportunitySnapshot,
} from "@/types/snapshot";
import { fmtMoney } from "@/utils/format";

/**
 * TenderBody renders kind=tender attributes: procurement_domain,
 * bidder_eligibility, plus the budget range derived from the universal
 * amount_min/amount_max. Deadline countdown is rendered in the
 * universal header upstream.
 */
export default function TenderBody({ snap }: { snap: OpportunitySnapshot }) {
  const domain = tenderProcurementDomain(snap);
  const bidderEligibility = stringAttr(snap, "bidder_eligibility");
  const submissionMethod = stringAttr(snap, "submission_method");
  const budget = fmtMoney(snap.amount_min, snap.amount_max, snap.currency, "");

  const description = snap.description ?? "";

  return (
    <>
      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-gray-600">
        {domain && (
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs capitalize">
            {domain}
          </span>
        )}
        {budget && (
          <span className="font-medium text-emerald-700">Budget: {budget}</span>
        )}
      </div>

      <dl className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
        {bidderEligibility && (
          <Field label="Bidder eligibility" value={bidderEligibility} />
        )}
        {submissionMethod && (
          <Field label="Submission method" value={submissionMethod} />
        )}
      </dl>

      {description && (
        <section
          className="prose prose-slate mt-8 max-w-none whitespace-pre-line"
          aria-label="Tender description"
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
