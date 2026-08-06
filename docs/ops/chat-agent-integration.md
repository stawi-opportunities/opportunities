# Chat-agent integration (matching + opportunity UI)

## What it is

**platform-chat-agent** (`service-profile/apps/chatagent`) is a product-agnostic
**conversational data-collection** tool. Matching is the first consumer.

| Layer | Value |
|-------|--------|
| Cloud Run app | `platform-chat-agent` (`cloud.deployment`) |
| Edge path | `https://api.stawi.org/chat-agent` |
| Audience | `/chat-agent` |
| Image | `ghcr.io/antinvestor/service-profile-chatagent` |

Products only change a **context definition** (fields + purpose). The engine
always re-evaluates **evidence** already present: seed fields, CV documents,
prior conversation, and structured inputs.

## Authorization model (do not get this wrong)

**Canonical platform ADR:**
`service-authentication` →
[`docs/adr/0002-product-peer-mesh-not-per-tenant-grants.md`](https://github.com/antinvestor/service-authentication/blob/main/docs/adr/0002-product-peer-mesh-not-per-tenant-grants.md)

### Two valid paths (platform baseline vs product BFF)

**Mode U — user JWT direct (platform baseline):** public SPA clients automatically
get `/chat-agent` audience; partition **members** have `chat_agent_turn` via OPL.
A logged-in user can call chat-agent with their own token for personal sessions
(`subject_id` = self). No SA grants, no per-tenant SQL.

**Mode B — Opportunities BFF (current product wiring for placement + job chat):**

```text
Browser (user JWT)  →  matching POST /me/chat
                     →  chat-agent CreateSession/Turn (matching SA JWT)
                            subject_id = candidate profile_id (request body)
```

| Actor | Token at chat-agent | Needs `/chat-agent`? | Needs `chat_agent_*`? |
|-------|---------------------|----------------------|------------------------|
| Candidate (SPA) — mode U | User JWT | Auto on public clients | `ROLE_MEMBER` (default) |
| Candidate (SPA) — mode B | No direct call | `/matching` only | Product access only |
| `opportunities-matching` bot — mode B | SA JWT | SA recipients + deploy | SA policy grants |

**New customers / tenants:** login + partition membership. **Never** write
per-tenant chat-agent grants. Platform self-service (mode U) works from membership;
product BFF (mode B) works once the **matching platform SA** peer contract is set.

### Three gates for matching → chat-agent (all required)

| Gate | Config | Wrong if missing |
|------|--------|------------------|
| 1. Request audience | `cloud.deployment` matching `requested_audience_paths` includes `/chat-agent` | Token mint omits `aud` |
| 2. Hydra whitelist | Tenancy `oauth_client_recipients` for client `opportunities-matching` → `https://api.stawi.org/chat-agent` | “audience has not been whitelisted” |
| 3. ReBAC | Matching SA policy grant `service_chat_agent` + perms used by RPCs (`chat_agent_turn`, and `chat_agent_view` / `chat_agent_manage` if List/UpsertContext) | `permission_denied` after token works |

Deploy env alone is **not** enough. Do not “fix chat for one customer” with SQL.

### Forbidden

- Per-customer / per-tenant migrations for chat-agent access  
- SPA direct calls to `/chat-agent` without an explicit product decision  
- Local heuristic LLM fallback when chat-agent is misconfigured (fail closed)  
- Hand Keto tuples in matching startup  

### Permissions matching uses

| RPC | Permission |
|-----|------------|
| `CreateSession`, `Turn`, `EndSession`, `IngestMessage` | `chat_agent_turn` |
| `GetSession` | `chat_agent_view` **or** `chat_agent_turn` |
| `UpsertContext` (best-effort register) | `chat_agent_manage` |
| `GetContext` / `ListContexts` | `chat_agent_view` |

## Contexts (matching)

| Key | When |
|-----|------|
| `stawi.placement.intake` | Onboarding + dashboard refine (`context=placement` or default) |
| `stawi.opportunity.view` | Opportunity detail side-chat (`context=opportunity` + `opportunity{…}`) |

Both are registered best-effort on first `/me/chat` via `UpsertContext`, with
**inline_config** fallback so cold starts still work.

## Matching env

```bash
CHAT_AGENT_SERVICE_URI=https://api.stawi.org/chat-agent
CHAT_AGENT_ENABLED=true
# Gate 1 only — still need Hydra recipients + SA grants (see Authorization model)
```

**Required.** There is no local `MeChatHandler` fallback.

| Failure | Response |
|---------|----------|
| `CHAT_AGENT_*` unset / client nil | `503 chat_agent_unavailable` |
| CreateSession error | `502 chat_agent_session_failed` |
| Turn error | `502 chat_agent_turn_failed` |

S2S client must attach a matching **service-account** token with audience
`https://api.stawi.org/chat-agent` (Connect interceptors or explicit token
source). A plain unauthenticated HTTP client will fail at chat-agent auth.

### Edge + deploy prerequisites

| Requirement | Where |
|-------------|--------|
| Edge route `/chat-agent` enabled with live `*.run.app` origin | `cloud.deployment` `edge/cloudflare-api-gateway/config/routes.prod.json` |
| Matching image pin (public GHCR) | `ghcr.io/stawi-opportunities/opportunities-matching:<tag>` in matching tfvars |
| Gate 1: `/chat-agent` in matching `requested_audience_paths` | `apps/opportunities-matching/cloudrun/envs/*.tfvars` |
| Gate 2–3: matching SA recipients + `service_chat_agent` grants | tenancy auth contract / ADR 0002 (platform SA, not per tenant) |
| GHCR packages public | Release runs `make-ghcr-public` (or org secret `GHCR_ADMIN_TOKEN`) |
| Cloud Run probes | Frame: startup `/readyz`, liveness `/livez` |

## SPA contracts

### Placement / onboarding

`POST /matching/me/chat` — unchanged fields (`message`, `history`, `draft`,
`cv_text`, `linkedin`).

### Opportunity side-chat

```json
{
  "message": "How do I fit this role?",
  "context": "opportunity",
  "opportunity": {
    "id": "…",
    "slug": "backend-engineer-acme",
    "title": "Backend Engineer",
    "issuing_entity": "Acme",
    "location": "Nairobi, KE",
    "description": "…",
    "kind": "job"
  },
  "draft": {},
  "history": []
}
```

`OpportunitySideChat` sends this automatically. Sessions are keyed per
candidate + context + opportunity slug so listing-specific threads stay separate.

### Conversation boundaries (important)

| Surface | Transcript | Seed | Persist |
|---------|------------|------|---------|
| Placement / onboarding (`context=placement`) | Intake only — collect matching profile fields | Prior intake messages + fields + CV | Full intake transcript + fields |
| Opportunity side-chat (`context=opportunity`) | **Separate per job** | Candidate fields + CV + job runtime/docs — **not** intake messages | Fields only (never overwrites intake messages) |

Onboarding must never continue a job conversation. Job chats may refine
placement fields using the candidate’s complete profile, but job Q&A does not
replace the conversation-grounded intake digest used for matching.

User bubbles always show clean text. Job context is supplied via `opportunity{}`
/ runtime — never as a `[Viewing opportunity: …]` prefix in the message body.

## Related jobs

`GET /api/opportunities/{slug}/related` (and `/api/jobs/{slug}/related`) returns
similar listings. The detail page renders them under **Similar jobs** via
`RelatedOpportunities`.

## Omnichannel = reuse Notification service

ChatAgent does **not** invent channels. It reuses the existing
`NotificationService.Send` client (same pattern as profile contact verification).
Channel routing lives in service-notification.

| Surface | How |
|---------|-----|
| Web SPA | `CreateSession` / `Turn` without `notification` — reply on RPC |
| SMS / email / … | Set `notification` (`type` + `recipient` ContactLink); replies → `Notification.Send` |
| Inbound adapter | `IngestMessage` with `NotificationTarget` + message |

```go
// SMS via Notification service (type/recipient match notification.v1.Notification)
cli.CreateSession(ctx, chatagentclient.CreateSessionRequest{
    SubjectID:  profileID,
    ContextKey: chatagentclient.ContextPlacementIntake,
    Notification: &chatagentclient.NotificationTarget{
        Type: chatagentclient.NotificationTypeSMS,
        Recipient: &chatagentclient.ContactLink{
            ContactID:   phoneContactID,
            ProfileID:   profileID,
            ProfileType: "Profile",
        },
        Language: "en",
    },
})

// Inbound message from a Notification adapter
cli.IngestMessage(ctx, chatagentclient.IngestMessageRequest{
    SubjectID:       profileID,
    ContextKey:      chatagentclient.ContextPlacementIntake,
    CreateIfMissing: true,
    Message:         userText,
    Notification: chatagentclient.NotificationTarget{
        Type: chatagentclient.NotificationTypeWhatsApp,
        Recipient: &chatagentclient.ContactLink{
            ContactID: waContactID,
            ProfileID: profileID,
        },
    },
})
```

Requires `NOTIFICATION_SERVICE_URI` on platform-chat-agent and `notification` in
catalog `requestedRecipients` for chat-agent.

## Deploy checklist

1. Merge `service-profile` chatagent + tag release → image + `ship-platform-chat-agent`
2. Apply `cloud.deployment` `platform-chat-agent` OpenTofu (Neon + Cloud Run + SM)
3. Update CF Worker `routes.prod.json` origin to the real `*.run.app` URL
4. **Peer contract (platform matching SA — once):** recipients + `service_chat_agent`
   grants; Hydra re-sync + SA policy applied (ADR 0002). Not per customer.
5. Ship matching with `CHAT_AGENT_*` env + Gate 1 audiences (tofu)
6. Smoke:
   - Matching logs: CreateSession succeeds (no Hydra audience whitelist error)
   - `POST /matching/me/chat` as a normal candidate (no extra grants)
   - Opportunity page: side-chat
   - (optional) `IngestChannelMessage` with notification client live
