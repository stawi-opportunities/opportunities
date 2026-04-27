// OnboardingRouter — flow-id → React component dispatcher.
//
// Each kind has at least one versioned flow ("job-onboarding-v1",
// "scholarship-onboarding-v1", …). Dynamic import keeps the four
// kinds the user isn't currently editing out of the initial bundle —
// the dashboard tabs trigger one chunk fetch per tab activation.

import { lazy, Suspense, type ComponentType } from "react";

type FlowComponent = ComponentType<{
  // The Flow components have kind-specific prefs types; the router
  // type-erases them since each tab carries its own narrow type at
  // the call site. Persistence happens at the dashboard level.
  initial?: unknown;
  onSubmit: (prefs: unknown) => void;
}>;

const flows: Record<string, () => Promise<{ default: FlowComponent }>> = {
  "job-onboarding-v1": () =>
    import("./job/v1/Flow") as Promise<{ default: FlowComponent }>,
  "scholarship-onboarding-v1": () =>
    import("./scholarship/v1/Flow") as Promise<{ default: FlowComponent }>,
  "tender-onboarding-v1": () =>
    import("./tender/v1/Flow") as Promise<{ default: FlowComponent }>,
  "deal-onboarding-v1": () =>
    import("./deal/v1/Flow") as Promise<{ default: FlowComponent }>,
  "funding-onboarding-v1": () =>
    import("./funding/v1/Flow") as Promise<{ default: FlowComponent }>,
};

export function OnboardingRouter({
  flowId,
  onSubmit,
  initial,
}: {
  flowId: string;
  onSubmit: (prefs: unknown) => void;
  initial?: unknown;
}) {
  const loader = flows[flowId];
  if (!loader) {
    return (
      <div className="rounded border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800">
        Unknown onboarding flow: <code>{flowId}</code>
      </div>
    );
  }
  // lazy() is keyed by reference, so we cache the component per flow id
  // outside this render to avoid re-creating it on every mount.
  const LazyFlow = getOrCreateLazy(flowId, loader);
  return (
    <Suspense fallback={<div className="text-sm text-gray-500">Loading…</div>}>
      <LazyFlow onSubmit={onSubmit} initial={initial} />
    </Suspense>
  );
}

const lazyCache = new Map<string, ReturnType<typeof lazy<FlowComponent>>>();

function getOrCreateLazy(
  flowId: string,
  loader: () => Promise<{ default: FlowComponent }>,
): ReturnType<typeof lazy<FlowComponent>> {
  const hit = lazyCache.get(flowId);
  if (hit) return hit;
  const created = lazy(loader);
  lazyCache.set(flowId, created);
  return created;
}
