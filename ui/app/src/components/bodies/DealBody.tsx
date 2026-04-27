import {
  dealDiscountPercent,
  type OpportunitySnapshot,
} from "@/types/snapshot";

/**
 * DealBody renders kind=deal attributes: discount_percent, expiry,
 * coupon_code, redemption_countries. Universal header upstream renders
 * the deadline (which doubles as the deal expiry).
 */
export default function DealBody({ snap }: { snap: OpportunitySnapshot }) {
  const discount = dealDiscountPercent(snap);
  const couponCode = stringAttr(snap, "coupon_code");
  const expiry = stringAttr(snap, "expiry") ?? snap.deadline;
  const redemptionCountries = stringArrayAttr(snap, "redemption_countries");

  const description = snap.description ?? "";

  return (
    <>
      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-gray-600">
        {typeof discount === "number" && (
          <span className="rounded-full bg-emerald-100 px-2 py-0.5 text-xs font-semibold text-emerald-800">
            {discount}% off
          </span>
        )}
        {expiry && (
          <span className="text-orange-700">
            Expires {new Date(expiry).toLocaleDateString()}
          </span>
        )}
      </div>

      <dl className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
        {couponCode && (
          <Field
            label="Coupon code"
            value={couponCode}
            mono
          />
        )}
        {redemptionCountries.length > 0 && (
          <Field label="Redeemable in" value={redemptionCountries.join(", ")} />
        )}
      </dl>

      {description && (
        <section
          className="prose prose-slate mt-8 max-w-none whitespace-pre-line"
          aria-label="Deal description"
        >
          {description}
        </section>
      )}
    </>
  );
}

function Field({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-xs font-medium uppercase tracking-wide text-gray-500">{label}</dt>
      <dd className={mono ? "mt-1 font-mono text-sm text-gray-900" : "mt-1 text-sm text-gray-900"}>
        {value}
      </dd>
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
