import {
  scholarshipDegreeLevel,
  scholarshipFieldOfStudy,
  type OpportunitySnapshot,
} from "@/types/snapshot";
import { fmtMoney } from "@/utils/format";

/**
 * ScholarshipBody renders kind=scholarship attributes: degree_level,
 * field_of_study, gpa_min, eligible_nationalities, plus the funding
 * stipend range derived from the universal amount_min/amount_max.
 * Deadline countdown is shown in the universal header upstream; here
 * we only render kind-specific fields.
 */
export default function ScholarshipBody({ snap }: { snap: OpportunitySnapshot }) {
  const degree = scholarshipDegreeLevel(snap);
  const field = scholarshipFieldOfStudy(snap);
  const gpaMin = numberAttr(snap, "gpa_min");
  const eligible = stringArrayAttr(snap, "eligible_nationalities");
  const stipend = fmtMoney(snap.amount_min, snap.amount_max, snap.currency, "year");

  const description = snap.description ?? "";

  return (
    <>
      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-gray-600">
        {degree && (
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs capitalize">
            {degree}
          </span>
        )}
        {field && (
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs">
            {field}
          </span>
        )}
        {stipend && (
          <span className="font-medium text-emerald-700">{stipend}</span>
        )}
      </div>

      <dl className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
        {typeof gpaMin === "number" && (
          <Field label="Minimum GPA" value={gpaMin.toFixed(2)} />
        )}
        {eligible.length > 0 && (
          <Field label="Eligible nationalities" value={eligible.join(", ")} />
        )}
      </dl>

      {description && (
        <section
          className="prose prose-slate mt-8 max-w-none whitespace-pre-line"
          aria-label="Scholarship description"
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

function numberAttr(snap: OpportunitySnapshot, key: string): number | undefined {
  const v = snap.attributes?.[key];
  return typeof v === "number" ? v : undefined;
}

function stringArrayAttr(snap: OpportunitySnapshot, key: string): string[] {
  const v = snap.attributes?.[key];
  if (!Array.isArray(v)) return [];
  return v.filter((x): x is string => typeof x === "string");
}
