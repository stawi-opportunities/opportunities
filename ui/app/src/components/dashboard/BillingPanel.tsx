import { planById, type PlanId } from "@/utils/plans";
import { Panel } from "./Panel";

export function BillingPanel({
  plan,
  renewsAt,
}: {
  plan: PlanId;
  renewsAt?: string;
}) {
  const info = planById(plan);
  return (
    <Panel title="Billing">
      <p className="text-sm text-gray-700">
        <span className="font-medium">{info.name}</span> · ${info.price}/month ·{" "}
        <span className="text-emerald-700">Active</span>
      </p>
      {renewsAt && (
        <p className="mt-1 text-xs text-gray-500">
          Renews on {new Date(renewsAt).toLocaleDateString()}
        </p>
      )}
      <a
        href="/pricing/"
        className="mt-3 inline-block text-sm font-medium text-accent-600 hover:text-accent-700"
      >
        Change plan or cancel →
      </a>
    </Panel>
  );
}
