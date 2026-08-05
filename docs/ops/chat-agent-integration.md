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
# OAuth: matching already requests audience /chat-agent (see cloud.deployment)
```

**Required.** There is no local `MeChatHandler` fallback.

| Failure | Response |
|---------|----------|
| `CHAT_AGENT_*` unset / client nil | `503 chat_agent_unavailable` |
| CreateSession error | `502 chat_agent_session_failed` |
| Turn error | `502 chat_agent_turn_failed` |

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

1. Merge `service-profile` feat/chatagent-app + tag release → image + `ship-platform-chat-agent`
2. Apply `cloud.deployment` `platform-chat-agent` OpenTofu (Neon + Cloud Run + SM)
3. Update CF Worker `routes.prod.json` origin to the real `*.run.app` URL
4. Ship matching with `CHAT_AGENT_*` env (already in `opportunities-matching` tofu)
5. Smoke:
   - `POST /chat-agent/chatagent.v1.ChatAgentService/ListContexts`
   - `POST /matching/me/chat` onboarding turn
   - Opportunity page: side-chat + related grid
   - (optional) `IngestChannelMessage` with `CHANNEL_SMS` + notification client live
