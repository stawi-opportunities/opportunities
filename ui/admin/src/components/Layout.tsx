import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { Link, Outlet, useLocation, useNavigate } from "react-router-dom";
import { authRuntime } from "@/api/admin-client";
import { useToast } from "@/components/ui/Toast";
import { useFocusTrap } from "@/hooks/useFocusTrap";

interface Breadcrumb {
  label: string;
  path?: string;
}

function useBreadcrumbs(): Breadcrumb[] {
  const { pathname } = useLocation();
  const segs = pathname.split("/").filter(Boolean);

  const labelMap: Record<string, string> = {
    definitions: "Definitions",
    sources: "Sources",
    variants: "Variants",
    opportunities: "Opportunities",
    seeds: "Seeds",
    digest: "Digest",
    jobs: "Jobs",
    rejections: "Rejections",
  };

  const crumbs: Breadcrumb[] = [{ label: "Ops", path: "/" }];

  if (segs.length === 0) return crumbs;

  if (segs[0] === "definitions") {
    crumbs.push({ label: "Definitions", path: "/definitions" });
    if (segs[1]) crumbs.push({ label: segs[1] });
    if (segs[2]) crumbs.push({ label: segs[2] });
    return crumbs;
  }

  if (segs[0] === "sources") {
    crumbs.push({ label: "Sources", path: "/sources" });
    if (segs[1]) crumbs.push({ label: segs[1] });
    return crumbs;
  }

  if (segs[0] === "jobs") {
    crumbs.push({ label: "Jobs", path: "/jobs" });
    return crumbs;
  }

  if (segs[0] === "rejections") {
    crumbs.push({ label: "Rejections", path: "/rejections" });
    return crumbs;
  }

  if (segs[0] === "variants" && segs[1]) {
    crumbs.push({ label: "Variants" }, { label: segs[1] });
    return crumbs;
  }

  if (segs[0] === "opportunities" && segs[1]) {
    crumbs.push({ label: "Opportunities" }, { label: segs[1] });
    return crumbs;
  }

  if (segs[0] === "seeds" && segs[1]) {
    crumbs.push({ label: "Seeds" }, { label: segs[1] }, { label: "Digest" });
    return crumbs;
  }

  for (const s of segs) {
    crumbs.push({ label: labelMap[s] ?? s });
  }
  return crumbs;
}

function useAuthUser() {
  const [email, setEmail] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    authRuntime()
      .getClaims()
      .then(
        (claims: Record<string, unknown>) => {
          if (!cancelled)
            setEmail(
              String(
                claims.email ?? claims.preferred_username ?? claims.sub ?? "",
              ),
            );
        },
        () => {
          /* not authenticated */
        },
      );
    return () => {
      cancelled = true;
    };
  }, []);

  return email;
}

const navLinkStyle = (active: boolean): React.CSSProperties => ({
  display: "flex",
  alignItems: "center",
  gap: "0.4rem",
  padding: "0.4rem 0.75rem",
  borderRadius: "var(--radius-md)",
  fontSize: "0.88rem",
  fontWeight: active ? 600 : 400,
  color: active ? "var(--c-primary)" : "var(--c-text)",
  background: active ? "#eef2ff" : "transparent",
  textDecoration: "none",
  transition: "background 0.15s",
});

const sidebarSection: React.CSSProperties = {
  padding: "0 0.75rem",
  marginBottom: "1.5rem",
};

const formInput: React.CSSProperties = {
  width: "100%",
  padding: "0.35rem 0.5rem",
  fontSize: "0.82rem",
  border: "1px solid var(--c-border)",
  borderRadius: "var(--radius-sm)",
  outline: "none",
  boxSizing: "border-box",
};

const sidebarStyle: React.CSSProperties = {
  position: "fixed",
  top: 0,
  left: 0,
  bottom: 0,
  width: "var(--sidebar-width)",
  background: "var(--c-surface)",
  borderRight: "1px solid var(--c-border)",
  display: "flex",
  flexDirection: "column",
  overflowY: "auto",
  zIndex: 100,
};

export function Layout() {
  const navigate = useNavigate();
  const location = useLocation();
  const crumbs = useBreadcrumbs();
  const userEmail = useAuthUser();
  const { toast } = useToast();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const sidebarRef = useRef<HTMLElement>(null);

  useFocusTrap(sidebarRef, sidebarOpen, () => setSidebarOpen(false));

  const goSlug = useCallback(
    (e: FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      const data = new FormData(e.currentTarget);
      const slug = String(data.get("slug") ?? "").trim();
      if (slug) {
        navigate(`/opportunities/${encodeURIComponent(slug)}`);
        setSidebarOpen(false);
      }
    },
    [navigate],
  );

  const goVariant = useCallback(
    (e: FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      const data = new FormData(e.currentTarget);
      const id = String(data.get("variant") ?? "").trim();
      if (id) {
        navigate(`/variants/${encodeURIComponent(id)}`);
        setSidebarOpen(false);
      }
    },
    [navigate],
  );

  const handleLogout = useCallback(async () => {
    try {
      await authRuntime().logout();
    } catch {
      toast("Logout failed", { type: "error" });
    }
  }, [toast]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (
        e.key === "/" &&
        !e.metaKey &&
        !e.ctrlKey &&
        !(
          e.target instanceof HTMLInputElement ||
          e.target instanceof HTMLTextAreaElement
        )
      ) {
        e.preventDefault();
        const input =
          document.querySelector<HTMLInputElement>('input[name="slug"]');
        input?.focus();
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, []);

  return (
    <>
      {/* Mobile sidebar overlay */}
      {sidebarOpen && (
        <div
          style={{
            position: "fixed",
            inset: 0,
            background: "rgba(0,0,0,0.3)",
            zIndex: 99,
          }}
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <aside ref={sidebarRef} id="sidebar" style={sidebarStyle}>
        <div
          style={{
            padding: "1rem 0.75rem",
            borderBottom: "1px solid var(--c-border)",
          }}
        >
          <Link
            to="/"
            style={{
              fontWeight: 700,
              fontSize: "1rem",
              color: "var(--c-text)",
              textDecoration: "none",
            }}
          >
            Stawi Admin
          </Link>
        </div>

        <nav style={{ flex: 1, paddingTop: "1rem" }}>
          <div style={sidebarSection}>
            <Link
              to="/"
              style={navLinkStyle(location.pathname === "/")}
              aria-current={location.pathname === "/" ? "page" : undefined}
            >
              Ops
            </Link>
            <Link
              to="/sources"
              style={navLinkStyle(
                location.pathname === "/sources" ||
                  location.pathname.startsWith("/sources/"),
              )}
              aria-current={
                location.pathname.startsWith("/sources") ? "page" : undefined
              }
            >
              Sources
            </Link>
            <Link
              to="/jobs"
              style={navLinkStyle(location.pathname.startsWith("/jobs"))}
              aria-current={
                location.pathname.startsWith("/jobs") ? "page" : undefined
              }
            >
              Jobs
            </Link>
            <Link
              to="/rejections"
              style={navLinkStyle(location.pathname.startsWith("/rejections"))}
              aria-current={
                location.pathname.startsWith("/rejections")
                  ? "page"
                  : undefined
              }
            >
              Rejections
            </Link>
            <Link
              to="/definitions"
              style={navLinkStyle(location.pathname.startsWith("/definitions"))}
              aria-current={
                location.pathname.startsWith("/definitions")
                  ? "page"
                  : undefined
              }
            >
              Definitions
            </Link>
          </div>

          <div
            style={{
              borderTop: "1px solid var(--c-border)",
              paddingTop: "0.75rem",
              ...sidebarSection,
            }}
          >
            <div
              style={{
                fontSize: "0.75rem",
                fontWeight: 600,
                textTransform: "uppercase",
                letterSpacing: "0.04em",
                color: "var(--c-text-secondary)",
                marginBottom: "0.5rem",
              }}
            >
              Quick jump
            </div>
            <form onSubmit={goSlug} style={{ marginBottom: "0.5rem" }}>
              <label
                htmlFor="quick-slug"
                style={{
                  position: "absolute",
                  width: 1,
                  height: 1,
                  overflow: "hidden",
                  clip: "rect(0,0,0,0)",
                }}
              >
                Opportunity slug
              </label>
              <input
                id="quick-slug"
                name="slug"
                placeholder="Opportunity slug…"
                style={formInput}
              />
            </form>
            <form onSubmit={goVariant}>
              <label
                htmlFor="quick-variant"
                style={{
                  position: "absolute",
                  width: 1,
                  height: 1,
                  overflow: "hidden",
                  clip: "rect(0,0,0,0)",
                }}
              >
                Variant ID
              </label>
              <input
                id="quick-variant"
                name="variant"
                placeholder="Variant id…"
                style={formInput}
              />
            </form>
          </div>
        </nav>

        {userEmail && (
          <div
            style={{
              padding: "0.75rem",
              borderTop: "1px solid var(--c-border)",
              fontSize: "0.8rem",
              color: "var(--c-text-secondary)",
            }}
          >
            <div
              style={{
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {userEmail}
            </div>
          </div>
        )}
      </aside>

      {/* Mobile hamburger */}
      <button
        type="button"
        onClick={() => setSidebarOpen((o) => !o)}
        style={{
          position: "fixed",
          top: "0.5rem",
          left: "0.5rem",
          zIndex: 110,
          background: sidebarOpen ? "transparent" : "var(--c-surface)",
          border: sidebarOpen ? "none" : "1px solid var(--c-border)",
          borderRadius: "var(--radius-md)",
          padding: "0.35rem 0.5rem",
          cursor: "pointer",
          fontSize: "1.1rem",
          lineHeight: 1,
          display: "none",
        }}
        className="sidebar-toggle"
        aria-label={sidebarOpen ? "Close sidebar" : "Open sidebar"}
        aria-expanded={sidebarOpen}
        aria-controls="sidebar"
      >
        {sidebarOpen ? "×" : "☰"}
      </button>

      {/* Main area */}
      <div
        style={{
          marginLeft: "var(--sidebar-width)",
          minHeight: "100vh",
        }}
        className="main-area"
      >
        {/* Breadcrumbs */}
        <div
          style={{
            padding: "0.75rem 2rem",
            borderBottom: "1px solid var(--c-border)",
            background: "var(--c-surface)",
            fontSize: "0.85rem",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <nav aria-label="Breadcrumb">
            {crumbs.map((c, i) => (
              <span key={i}>
                {i > 0 && (
                  <span
                    aria-hidden="true"
                    style={{
                      margin: "0 0.35rem",
                      color: "var(--c-text-secondary)",
                    }}
                  >
                    /
                  </span>
                )}
                {c.path ? (
                  <Link
                    to={c.path}
                    style={{ color: "var(--c-text-secondary)" }}
                  >
                    {c.label}
                  </Link>
                ) : (
                  <span
                    style={{ color: "var(--c-text)", fontWeight: 500 }}
                    aria-current={i === crumbs.length - 1 ? "page" : undefined}
                  >
                    {c.label}
                  </span>
                )}
              </span>
            ))}
          </nav>
          <button
            type="button"
            onClick={handleLogout}
            style={{
              background: "none",
              border: "none",
              cursor: "pointer",
              fontSize: "0.82rem",
              color: "var(--c-text-secondary)",
              padding: "0.2rem 0.5rem",
              borderRadius: "var(--radius-sm)",
            }}
            aria-label="Logout"
          >
            Logout
          </button>
        </div>

        {/* Page content */}
        <main id="main-content" style={{ padding: "1.5rem 2rem" }}>
          <Outlet />
        </main>
      </div>
    </>
  );
}
