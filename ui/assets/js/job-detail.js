// job-detail.js — hydrates /jobs/<slug>/ from a public R2 JobSnapshot.
//
// The page is served as a single shell HTML for every slug (via the CF Pages
// _redirects rule). This script reads the slug from location.pathname,
// fetches the snapshot JSON, and mounts the data into the DOM using safe DOM
// construction: createElement + textContent for dynamic fields.
//
// The snapshot's `description_html` field is sanitized server-side via
// bluemonday's UGC policy in pkg/publish/html.go BEFORE being written to R2.
// Rather than setting innerHTML (which linters and CSP rightly distrust), we
// parse the sanitized string with DOMParser and transfer the resulting nodes
// into the live document. DOMParser never executes scripts during parsing.

import { fetchSnapshot, CONTENT_ORIGIN } from "./api.js";

function slugFromPath() {
  const m = window.location.pathname.match(/^\/jobs\/([^/]+)\/?$/);
  return m ? decodeURIComponent(m[1]) : null;
}

function fmtMoney(min, max, currency) {
  if (!min && !max) return "";
  const c = currency || "USD";
  if (min && max && min !== max) {
    return `${c} ${min.toLocaleString()}–${max.toLocaleString()}`;
  }
  return `${c} ${(max || min).toLocaleString()}`;
}

function isoInPast(iso) {
  if (!iso) return false;
  return new Date(iso).getTime() < Date.now();
}

function span(cls, text) {
  const el = document.createElement("span");
  if (cls) el.className = cls;
  if (text != null && text !== "") el.textContent = text;
  return el;
}

function el(tag, cls, children = []) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  for (const c of children) {
    if (c == null) continue;
    if (typeof c === "string") n.appendChild(document.createTextNode(c));
    else n.appendChild(c);
  }
  return n;
}

/**
 * Parse server-sanitized HTML via DOMParser and move the parsed body children
 * into `target`. Safe because bluemonday stripped dangerous tags/attributes
 * before the HTML reached R2, and DOMParser does not execute anything.
 */
function mountSanitizedHTML(target, html) {
  if (!html) return;
  const doc = new DOMParser().parseFromString(String(html), "text/html");
  const frag = document.createDocumentFragment();
  for (const child of Array.from(doc.body.childNodes)) frag.appendChild(child);
  target.appendChild(frag);
}

function render(root, snap) {
  root.replaceChildren();

  const expired = isoInPast(snap.expires_at);
  if (expired) {
    root.appendChild(
      el(
        "div",
        "mb-4 rounded border border-amber-300 bg-amber-100 px-4 py-2 text-amber-900",
        ["This job is no longer accepting applications."],
      ),
    );
  }

  // Header
  const header = el("header", "mb-6");
  header.appendChild(
    span("text-sm font-medium text-gray-500", snap.company?.name || ""),
  );
  const title = document.createElement("h1");
  title.className = "mt-1 text-3xl font-bold text-gray-900";
  title.textContent = snap.title || "";
  header.appendChild(title);

  const metaRow = el("div", "mt-3 flex flex-wrap items-center gap-2 text-sm text-gray-600");
  if (snap.location?.text) metaRow.appendChild(span("", snap.location.text));
  if (snap.employment?.type)
    metaRow.appendChild(span("rounded bg-gray-100 px-2 py-0.5", snap.employment.type));
  if (snap.employment?.seniority)
    metaRow.appendChild(span("rounded bg-gray-100 px-2 py-0.5", snap.employment.seniority));
  const money = fmtMoney(
    snap.compensation?.min,
    snap.compensation?.max,
    snap.compensation?.currency,
  );
  if (money) metaRow.appendChild(span("font-medium text-gray-700", money));
  header.appendChild(metaRow);
  root.appendChild(header);

  // Skills
  if (snap.skills?.required?.length || snap.skills?.nice_to_have?.length) {
    const skillsSection = el("section", "mb-6");
    if (snap.skills.required?.length) {
      skillsSection.appendChild(
        el("h2", "text-sm font-semibold text-gray-700", ["Required skills"]),
      );
      const ul = el("ul", "mt-2 flex flex-wrap gap-2");
      for (const s of snap.skills.required) {
        ul.appendChild(
          el("li", "rounded-full bg-blue-50 px-3 py-1 text-sm text-blue-800", [s]),
        );
      }
      skillsSection.appendChild(ul);
    }
    if (snap.skills.nice_to_have?.length) {
      skillsSection.appendChild(
        el("h2", "mt-4 text-sm font-semibold text-gray-700", ["Nice to have"]),
      );
      const ul = el("ul", "mt-2 flex flex-wrap gap-2");
      for (const s of snap.skills.nice_to_have) {
        ul.appendChild(
          el("li", "rounded-full bg-gray-100 px-3 py-1 text-sm text-gray-700", [s]),
        );
      }
      skillsSection.appendChild(ul);
    }
    root.appendChild(skillsSection);
  }

  // Description — sanitized HTML mounted via DOMParser (no innerHTML assignment).
  const descWrap = el("section", "prose max-w-none");
  mountSanitizedHTML(descWrap, snap.description_html);
  root.appendChild(descWrap);

  // Apply button
  if (snap.apply_url && !expired) {
    const a = document.createElement("a");
    a.href = snap.apply_url;
    a.target = "_blank";
    a.rel = "noopener noreferrer";
    a.className = "mt-6 inline-block rounded bg-blue-600 px-5 py-3 text-white hover:bg-blue-700";
    a.textContent = "Apply now";
    root.appendChild(a);
  }

  document.title = `${snap.title || "Job"} · ${snap.company?.name || "Stawi Jobs"}`;
}

function renderNotFound(root) {
  root.replaceChildren(
    el("div", "mx-auto max-w-md py-16 text-center", [
      el("h1", "text-2xl font-semibold text-gray-900", ["Job not found"]),
      el("p", "mt-2 text-gray-600", [
        "This job has been removed or has expired.",
      ]),
      (() => {
        const a = document.createElement("a");
        a.href = "/search/";
        a.className = "mt-6 inline-block text-blue-600 hover:underline";
        a.textContent = "Back to search";
        return a;
      })(),
    ]),
  );
  document.title = "Job not found · Stawi Jobs";
}

function renderError(root) {
  root.replaceChildren(
    el("p", "mt-8 text-red-700", [
      "Unable to load this job right now. Please try again.",
    ]),
  );
}

export async function init() {
  const root = document.getElementById("job-detail-root");
  if (!root) return;
  const slug = slugFromPath();
  if (!slug) return renderError(root);
  try {
    const snap = await fetchSnapshot(slug);
    if (!snap) return renderNotFound(root);
    render(root, snap);
  } catch (e) {
    console.error("job-detail init", e);
    renderError(root);
  }
}

// Auto-boot on /jobs/<slug>/.
if (typeof window !== "undefined" && /^\/jobs\/[^/]+\/?$/.test(window.location.pathname)) {
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => { init(); });
  } else {
    init();
  }
}

export { CONTENT_ORIGIN };
