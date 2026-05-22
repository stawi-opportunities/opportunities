# Local autoapply walkthrough

End-to-end run of the session-capture + autoapply flow on one workstation:
**UI → extension → matching → autoapply**, against your own browser's
BrighterMonday session. Five terminals, ~15 minutes once the infra
image is cached.

Matching is included because that's where the `/pairings` and
`/candidates/me/sessions/*` endpoints live, but it boots in **degraded
mode** (Iceberg disabled) so you don't need Lakekeeper. The matching
pipeline itself is off — the only matching feature we exercise is the
session-capture HTTP surface.

## Prerequisites

- Docker daemon running (`docker info` succeeds)
- Go 1.22+ on `PATH`
- Node 20+ on `PATH` (via nvm: `source ~/.nvm/nvm.sh`)
- Hugo binary at `bin/hugo` (run `make $(pwd)/bin/hugo` once to download)
- Chrome or Firefox with developer-mode extensions enabled

## Terminal layout

Open five terminals. Each runs one long-lived process:

| Terminal | Purpose | Port |
|---|---|---|
| 1 | Infra (Postgres + Redis + NATS) | `5432`, `6379`, `4222` |
| 2 | `matching` (session endpoints) | `:8082` |
| 3 | `autoapply` (devmode + intent endpoint) | `:8080` |
| 4 | UI dev server (Hugo + Vite) | `:5170`, `:5173` |
| 5 | Free for `psql` / `curl` / `tail` |

## Step 1 — Generate crypto keys (once)

Each is 32 random bytes, base64-encoded. The matching service won't
start without both; autoapply needs only `SESSION_MASTER_KEY`.

```bash
echo "SESSION_MASTER_KEY=$(openssl rand -base64 32)" >> .env.local
echo "SESSION_TOKEN_KEY=$(openssl rand -base64 32)" >> .env.local
echo "SESSION_MASTER_KEY_ID=v1" >> .env.local
```

`.env.local` is gitignored; we'll `source` it from each service shell.

## Step 2 — Terminal 1: infra

```bash
docker compose -f deploy/docker-compose.yml up -d postgres redis nats
docker compose -f deploy/docker-compose.yml ps
```

You should see three containers `Up`. Postgres on `5432`, Redis on
`6379`, NATS on `4222`.

## Step 3 — One-shot DB migration

Matching's `DO_MIGRATION=true` path runs `AutoMigrate` for every model
it touches (`candidate_profiles`, `candidate_applications`,
`candidate_sessions`, `opportunity_flags`) then exits. Apply the
applications-OLTP migration on top (raw SQL from main):

```bash
go build -o bin/matching ./apps/matching/cmd

source .env.local

DATABASE_URL='postgres://stawi:stawi@127.0.0.1:5432/stawijobs?sslmode=disable' \
DO_MIGRATION=true \
SESSION_MASTER_KEY="$SESSION_MASTER_KEY" \
SESSION_TOKEN_KEY="$SESSION_TOKEN_KEY" \
SOURCE_AUTH_DIR="$PWD/definitions/source-auth" \
OPPORTUNITY_KINDS_DIR="$PWD/definitions/opportunity-kinds" \
./bin/matching | tail -10

docker exec -i deploy-postgres-1 psql -U stawi -d stawijobs < db/migrations/0010_applications_oltp.sql
docker exec -i deploy-postgres-1 psql -U stawi -d stawijobs < db/migrations/0014_applications_phase3.sql

# Sanity check — expect 9 tables including candidate_sessions and applications
docker exec deploy-postgres-1 psql -U stawi -d stawijobs -c '\dt'
```

## Step 4 — Terminal 4: UI

In one of your terminals (it doesn't need to be terminal 4 specifically,
just keep it running):

```bash
source ~/.nvm/nvm.sh
make ui-dev
```

Hugo listens on `:5170`, Vite on `:5173`. Open
<http://localhost:5170/connected/> in your browser. You should see the
"Connected accounts" shell render. Click **Sign in** — this hits the
dev Hydra (`opportunities-web-dev` client) and bounces you back signed
in.

## Step 5 — Seed your candidate row

You need a `candidate_profiles` row whose `profile_id` matches the
`sub` claim of the JWT you just got from Hydra. Two ways to grab it:

**Easy:** Open your browser's DevTools console on the Connected
Accounts page and run:

```js
await stawi_auth.getClaims().then(c => c.sub)
```

(replace `stawi_auth` with whatever the runtime exposes — check
[ui/app/src/auth/runtime.ts](../ui/app/src/auth/runtime.ts) if not).

**Pragmatic alternative:** the Connected Accounts page will 403 on
`/pairings` if the row is missing; the response body shows your
profile_id. Or hit Hydra's userinfo endpoint manually.

Then:

```bash
./scripts/seed-dev-candidate.sh <your-hydra-sub>
```

The script inserts (or updates) `candidate_profiles` with
`id=cnd_dev_1`, `profile_id=<sub>`, `subscription=paid`,
`auto_apply=true`.

## Step 6 — Terminal 2: matching (session-capture)

```bash
source .env.local

DATABASE_URL='postgres://stawi:stawi@127.0.0.1:5432/stawijobs?sslmode=disable' \
VALKEY_URL='redis://localhost:6379' \
NATS_URL='nats://127.0.0.1:4222' \
HTTP_PORT=':8082' \
SESSION_MASTER_KEY="$SESSION_MASTER_KEY" \
SESSION_TOKEN_KEY="$SESSION_TOKEN_KEY" \
SOURCE_AUTH_DIR="$PWD/definitions/source-auth" \
OPPORTUNITY_KINDS_DIR="$PWD/definitions/opportunity-kinds" \
./bin/matching
```

Expected log lines:

```
candidates: ICEBERG_CATALOG_URI unset — /candidates/match + /_admin/cv/stale_nudge disabled
matching: valkey debouncer enabled
session-capture: enabled valkey=true
listening on server port http port=:8082
```

Sanity check:

```bash
curl -sS localhost:8082/healthz
curl -sS localhost:8082/sources/auth-manifest | jq '.sources[].source_type'
```

The second call should print `brightermonday` and `jobberman`.

## Step 7 — Terminal 3: autoapply (devmode + intent endpoint)

Build with the `devmode` tag so the `POST /dev/intent` endpoint mounts:

```bash
go build -tags=devmode -o bin/autoapply ./apps/autoapply/cmd
```

Run:

```bash
source .env.local

DATABASE_URL='postgres://stawi:stawi@127.0.0.1:5432/stawijobs?sslmode=disable' \
NATS_URL='nats://127.0.0.1:4222' \
AUTO_APPLY_QUEUE_URL='nats://127.0.0.1:4222/svc.opportunities.autoapply.submit.v1' \
HTTP_PORT=':8080' \
AUTO_APPLY_ENABLED=true \
AUTO_APPLY_DRY_RUN=false \
AUTO_APPLY_DEV_INTENT_ENDPOINT=true \
AUTO_APPLY_DEV_ALLOW_INSECURE_CV=true \
SESSION_MASTER_KEY="$SESSION_MASTER_KEY" \
SESSION_MASTER_KEY_ID="$SESSION_MASTER_KEY_ID" \
SOURCE_AUTH_DIR="$PWD/definitions/source-auth" \
./bin/autoapply
```

Expected:

```
autoapply: source-auth manifests loaded source_auth_count=2
autoapply: session-replay submitter enabled
autoapply: POST /dev/intent enabled — devmode build only
autoapply: service starting
```

**`AUTO_APPLY_DRY_RUN=true` is the safer default.** Keep dry-run for
the first run; you'll see autoapply consume the intent and skip with
`session_required` until you've actually captured one. The tracker
bridge into `applications` only fires on real submissions — skip the
dry-run if you want to see that row populate.

## Step 8 — Load the extension

In Chrome: `chrome://extensions/` → toggle **Developer mode** →
**Load unpacked** → pick `./extension/`. The extension icon appears in
your toolbar.

Click the icon. The popup shows the "paste your 6-character code" form.

## Step 9 — Pair the extension

In the UI's Connected Accounts page (signed in), click **Pair extension**.
A 6-character code appears (5 minute TTL).

Type the code into the extension popup → **Pair**.

The popup should flip to **Extension paired ✓** and list BrighterMonday
+ Jobberman as supported sources.

Watch terminal 2 — you'll see:

```
POST /pairings  candidate_id=cnd_dev_1
POST /pairings/redeem  (single-use; code consumed)
```

## Step 10 — Capture your real BrighterMonday session

Open a new tab, go to <https://www.brightermonday.co.ke/login>, and
sign into your BrighterMonday account.

Once you're signed in, the extension's `chrome.cookies.onChanged`
listener fires. After a 1.5s debounce it:

1. Hits `https://www.brightermonday.co.ke/api/v1/me` to confirm logged-in
2. Captures the cookies for `.brightermonday.co.ke`
3. POSTs `/candidates/me/sessions/brightermonday` to matching with the
   Stawi access token

Watch terminal 2:

```
POST /candidates/me/sessions/brightermonday  candidate_id=cnd_dev_1
session-capture: accepted source=brightermonday
```

Refresh the Connected Accounts page. The BrighterMonday card should
show **Connected ✓**.

## Step 11 — Fire the autoapply intent

You need a real BrighterMonday job URL whose form pattern matches the
manifest. From the manifest at
[definitions/source-auth/brightermonday.yaml:31](../definitions/source-auth/brightermonday.yaml#L31):

```
form_url_pattern: ^https://www\.brightermonday\.co\.ke/job/.+/apply$
```

So `https://www.brightermonday.co.ke/job/<some-id>/apply` is what we
need. Pick a job from the listings — for a first-run **pick a job you
don't mind submitting an application to** (or set
`AUTO_APPLY_DRY_RUN=true` first; with dry-run the tracker bridge does
NOT fire, but you see autoapply attempt the session lookup).

```bash
curl -X POST localhost:8080/dev/intent \
     -H 'content-type: application/json' \
     -d '{
       "candidate_id":"cnd_dev_1",
       "canonical_job_id":"job_dev_test_1",
       "apply_url":"https://www.brightermonday.co.ke/job/<paste-real-id>/apply",
       "source_type":"brightermonday",
       "full_name":"Your Name",
       "email":"you@example.com",
       "phone":"+254700000000"
     }'
```

Watch terminal 3:

```
autoapply: attempt complete  method=session_replay  status=submitted
autoapply: applications tracker bridge wrote to applications-OLTP
```

(or status=skipped, skip_reason=session_required if step 10 didn't
land yet — check terminal 2 for cookie capture warnings.)

## Step 12 — Inspect both DB tables

```bash
docker exec deploy-postgres-1 psql -U stawi -d stawijobs -c "
  SELECT id, status, method, submitted_at
    FROM candidate_applications
   WHERE candidate_id = 'cnd_dev_1'
   ORDER BY created_at DESC LIMIT 5;
"

docker exec deploy-postgres-1 psql -U stawi -d stawijobs -c "
  SELECT application_id, status, metadata->>'source_type' AS src, metadata->>'method' AS method, submitted_at
    FROM applications
   WHERE candidate_id = 'cnd_dev_1'
   ORDER BY created_at DESC LIMIT 5;
"
```

Both tables should show the row. The `applications` row is the one
visible to the candidate dashboard.

## Cleanup

```bash
pkill -f "bin/matching$"
pkill -f "bin/autoapply$"
pkill -f "vite|hugo server"
docker compose -f deploy/docker-compose.yml down
```

## Troubleshooting

**Matching exits with `iceberg catalog load failed`** — the
`ICEBERG_CATALOG_URI` env var got set to something non-empty. Unset
it and re-run; the degraded mode kicks in only when it's empty.

**Extension popup shows "no candidate profile for this user"** — the
JWT sub claim doesn't match any `profile_id` in `candidate_profiles`.
Re-run `scripts/seed-dev-candidate.sh` with the correct sub.

**`/pairings` returns 401** — the UI's `runtime.fetch` isn't sending a
Bearer header. Sign in via the page's CTA button; check DevTools
Network for the `Authorization: Bearer ...` header on the failing
request.

**Extension uploads return 404 unknown_source** — the manifest registry
doesn't know about that source. `SOURCE_AUTH_DIR` likely points at the
wrong place. Set it to `$PWD/definitions/source-auth` and restart
matching.

**Replay returns `skipped/session_expired` immediately** — the captured
cookies don't grant access to the apply URL. BrighterMonday might use
a separate session token for the apply form, or the user account
isn't allowed to apply to that job. Capture again after a fresh login.
