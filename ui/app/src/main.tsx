import { StrictMode, type ComponentType, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { AppProviders } from "@/providers/AppProviders";

// Every island is a [id → component] pair. Only the components whose mount
// target exists on the page get rendered. Components are lazy-imported so
// a page that only uses <Nav> doesn't pay for <Onboarding>'s form library.

type Island = {
  id: string;
  component: () => Promise<{ default: ComponentType<unknown> }>;
};

const islands: Island[] = [
  { id: "mount-nav",            component: () => import("@/components/Nav") },
  { id: "mount-job-detail",     component: () => import("@/components/JobDetail") },
  { id: "mount-search",         component: () => import("@/components/Search") },
  { id: "mount-job-list",       component: () => import("@/components/JobList") },
  { id: "mount-locale-shard",   component: () => import("@/components/LocaleShard") },
  { id: "mount-category-index", component: () => import("@/components/CategoryIndex") },
  { id: "mount-category-page",  component: () => import("@/components/CategoryPage") },
  { id: "mount-dashboard",      component: () => import("@/pages/Dashboard") },
  { id: "mount-onboarding",     component: () => import("@/pages/Onboarding") },
  { id: "mount-auth-callback",  component: () => import("@/components/AuthCallback") },
];

async function hydrate(island: Island, el: HTMLElement) {
  const mod = await island.component();
  const Component = mod.default;
  createRoot(el).render(
    <StrictMode>
      <AppProviders>
        <Suspense fallback={null}>
          <Component />
        </Suspense>
      </AppProviders>
    </StrictMode>,
  );
}

function boot() {
  for (const island of islands) {
    const el = document.getElementById(island.id);
    if (el) void hydrate(island, el);
  }
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", boot);
} else {
  boot();
}
