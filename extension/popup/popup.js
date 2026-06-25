// Popup glue. Three states (unpaired / paired / loading) and a
// per-source status query that drives the badge + the action button.
//
// The popup is small enough that we hand-roll the rendering instead
// of pulling in a framework. Every render() reads the latest tokens
// + manifest + per-source login statuses and rebuilds the source
// list in place.

import { redeemPairingCode, HttpError } from "../lib/api.js";
import { getTokens, getManifest } from "../lib/storage.js";

const $ = (id) => document.getElementById(id);
const sections = {
  loading: $("loading"),
  unpaired: $("unpaired"),
  paired: $("paired"),
};
const pairForm = $("pair-form");
const pairCode = $("pair-code");
const pairError = $("pair-error");
const pairSubmit = pairForm.querySelector("button[type=submit]");
const sourceList = $("source-list");
const refreshBtn = $("refresh");

let busyPair = false;
let inflightCapture = new Set(); // source_types currently being captured

// ─── State machine ──────────────────────────────────────────────

function show(name) {
  for (const [key, el] of Object.entries(sections)) {
    el.hidden = key !== name;
  }
  refreshBtn.hidden = name !== "paired";
}

// ─── Render: paired ─────────────────────────────────────────────

function renderSources(sources, statuses) {
  const statusByType = new Map((statuses || []).map((s) => [s.source_type, s]));
  sourceList.innerHTML = "";

  if (!sources.length) {
    const li = document.createElement("li");
    li.className = "muted small";
    li.textContent = "No sources available yet.";
    sourceList.appendChild(li);
    return;
  }

  for (const s of sources) {
    const status = statusByType.get(s.source_type) || {};
    sourceList.appendChild(renderSourceCard(s, status));
  }
}

function renderSourceCard(source, status) {
  const li = document.createElement("li");
  li.className = "source";

  // Header — name + status badge.
  const head = document.createElement("div");
  head.className = "source-head";

  const name = document.createElement("span");
  name.className = "source-name";
  name.textContent = source.display_name || source.source_type;

  head.appendChild(name);
  head.appendChild(makeBadge(status));
  li.appendChild(head);

  // Actions.
  const actions = document.createElement("div");
  actions.className = "source-actions";

  if (status.logged_in === false) {
    // Not logged in → primary CTA opens the source's login page.
    const a = document.createElement("a");
    a.className = "primary";
    a.href = source.login_url;
    a.target = "_blank";
    a.rel = "noopener noreferrer";
    a.textContent = "Sign in";
    actions.appendChild(a);
  } else {
    // Logged in or unknown → Connect (force a capture now).
    const btn = document.createElement("button");
    btn.className = status.logged_in ? "primary" : "";
    btn.textContent = inflightCapture.has(source.source_type)
      ? "Capturing…"
      : "Connect this account";
    btn.disabled = inflightCapture.has(source.source_type);
    btn.addEventListener("click", () => onConnectClick(source, btn));
    actions.appendChild(btn);

    // Always offer a secondary "Open source" link.
    const a = document.createElement("a");
    a.href = source.home_url || source.login_url;
    a.target = "_blank";
    a.rel = "noopener noreferrer";
    a.textContent = "Open";
    actions.appendChild(a);
  }

  li.appendChild(actions);
  return li;
}

function makeBadge(status) {
  const badge = document.createElement("span");
  badge.className = "badge";
  if (status.captured) {
    badge.classList.add("captured");
    badge.textContent = "Connected";
  } else if (status.logged_in === true) {
    badge.classList.add("connected");
    badge.textContent = "Logged in";
  } else if (status.logged_in === false) {
    badge.classList.add("notlogged");
    badge.textContent = "Not signed in";
  } else {
    badge.classList.add("unknown");
    badge.textContent = "Checking…";
  }
  return badge;
}

// ─── Actions ────────────────────────────────────────────────────

async function onConnectClick(source, button) {
  inflightCapture.add(source.source_type);
  button.disabled = true;
  button.textContent = "Capturing…";

  const res = await sendMessage({
    type: "stawi:capture-now",
    source_type: source.source_type,
  });

  inflightCapture.delete(source.source_type);

  if (res?.ok) {
    // Re-render with the latest status; the badge will flip and the
    // button will say "Re-capture" so a re-click works.
    await renderPaired();
  } else {
    const reason = res?.reason || "";
    button.disabled = false;
    if (reason === "not_logged_in") {
      // Direct the user to sign in first.
      button.textContent = "Sign in first ↗";
      button.onclick = () => window.open(source.login_url, "_blank");
    } else if (reason === "no_cookies") {
      button.textContent = "No session cookies yet";
    } else if (reason === "needs_repair") {
      // Tokens were cleared — popup will flip back to unpaired on next render.
      await render();
    } else {
      button.textContent = "Retry";
    }
  }
}

// ─── Render orchestration ───────────────────────────────────────

async function renderPaired() {
  // Ask the background to refresh the cached manifest first so a new
  // source YAML on the server appears without an extension reload.
  await sendMessage({ type: "stawi:refresh-manifest" });
  const manifest = await getManifest();
  const sources = manifest?.sources || [];

  // Render with no status info first so the user sees something
  // immediately, then fill in the badges when the background returns.
  renderSources(sources, []);

  const statusRes = await sendMessage({ type: "stawi:status" });
  renderSources(sources, statusRes?.statuses || []);
}

async function render() {
  show("loading");
  const tokens = await getTokens();
  if (!tokens?.access_token) {
    show("unpaired");
    pairCode.focus();
    return;
  }
  show("paired");
  await renderPaired();
}

// ─── Pair flow ──────────────────────────────────────────────────

pairForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (busyPair) return;
  pairError.hidden = true;
  const code = pairCode.value.trim().toUpperCase();
  if (code.length !== 6) {
    showPairError("Code must be 6 characters.");
    return;
  }
  setPairBusy(true);
  try {
    await redeemPairingCode(code);
    await render();
  } catch (err) {
    if (err instanceof HttpError && err.status === 404) {
      showPairError("Code not found or expired. Request a fresh one from Stawi.");
    } else if (err instanceof HttpError && err.status === 429) {
      showPairError("Too many attempts. Wait a moment and try again.");
    } else {
      showPairError("Pair failed. Check that Stawi services are running.");
    }
  } finally {
    setPairBusy(false);
  }
});

function setPairBusy(busy) {
  busyPair = busy;
  pairSubmit.disabled = busy;
  pairSubmit.querySelector(".btn-label").textContent = busy ? "Pairing…" : "Pair";
  pairSubmit.querySelector(".btn-spinner").hidden = !busy;
}

function showPairError(msg) {
  pairError.textContent = msg;
  pairError.hidden = false;
}

refreshBtn.addEventListener("click", () => render());

// ─── Bootstrap ──────────────────────────────────────────────────

function sendMessage(msg) {
  return new Promise((resolve) => chrome.runtime.sendMessage(msg, resolve));
}

void render();
