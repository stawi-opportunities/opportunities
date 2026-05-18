import { redeemPairingCode, HttpError } from "../lib/api.js";
import { getTokens, getManifest } from "../lib/storage.js";

const unpairedSection = document.getElementById("unpaired");
const pairedSection = document.getElementById("paired");
const pairForm = document.getElementById("pair-form");
const pairCode = document.getElementById("pair-code");
const pairError = document.getElementById("pair-error");
const sourceList = document.getElementById("source-list");

async function render() {
  const tokens = await getTokens();
  if (!tokens?.access_token) {
    unpairedSection.hidden = false;
    pairedSection.hidden = true;
    return;
  }
  unpairedSection.hidden = true;
  pairedSection.hidden = false;
  await renderSources();
}

async function renderSources() {
  // Ask the background worker to refresh first so a new manifest
  // entry shows up without a popup reload race.
  await sendMessage({ type: "stawi:refresh-manifest" });
  const manifest = await getManifest();
  sourceList.innerHTML = "";
  const sources = manifest?.sources || [];
  if (sources.length === 0) {
    const li = document.createElement("li");
    li.className = "muted small";
    li.textContent = "No sources available yet.";
    sourceList.appendChild(li);
    return;
  }
  for (const s of sources) {
    const li = document.createElement("li");
    const name = document.createElement("span");
    name.textContent = s.display_name || s.source_type;
    const btn = document.createElement("button");
    btn.className = "capture-btn";
    btn.textContent = "Connect this account";
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      btn.textContent = "Capturing…";
      const res = await sendMessage({
        type: "stawi:capture-now",
        source_type: s.source_type,
      });
      btn.disabled = false;
      btn.textContent = res?.ok ? "Captured ✓" : "Retry";
    });
    li.appendChild(name);
    li.appendChild(btn);
    sourceList.appendChild(li);
  }
}

pairForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  pairError.hidden = true;
  const code = pairCode.value.trim();
  if (code.length !== 6) {
    showError("Code must be 6 characters.");
    return;
  }
  try {
    await redeemPairingCode(code);
    await render();
  } catch (err) {
    if (err instanceof HttpError && err.status === 404) {
      showError("Code not found. It may have expired — request a fresh one from Stawi.");
    } else if (err instanceof HttpError && err.status === 429) {
      showError("Too many attempts. Wait a moment and try again.");
    } else {
      showError("Pair failed. Try again in a few seconds.");
    }
  }
});

function showError(msg) {
  pairError.textContent = msg;
  pairError.hidden = false;
}

function sendMessage(msg) {
  return new Promise((resolve) => chrome.runtime.sendMessage(msg, resolve));
}

void render();
