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

When disabled or URI empty, `/me/chat` uses the local `MeChatHandler` (LLM/heuristic).
Agent errors also fall back to local so the SPA never hard-fails.

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

## Related jobs

`GET /api/opportunities/{slug}/related` (and `/api/jobs/{slug}/related`) returns
similar listings. The detail page renders them under **Similar jobs** via
`RelatedOpportunities`.

## Omnichannel (Notification)

ChatAgent is **channel-agnostic**. The engine only produces text; **Notification**
delivers non-web replies (SMS, WhatsApp, email, push, in-app, USSD).

| Surface | How |
|---------|-----|
| Web SPA | `CreateSession` / `Turn` without `channel` — reply on RPC |
| SMS / WhatsApp / … | Set `channel` on `CreateSession`; replies auto-`Notification.Send` |
| Inbound adapter | `IngestChannelMessage` with channel binding + message |

```go
// Outbound-capable SMS session
cli.CreateSession(ctx, chatagentclient.CreateSessionRequest{
    SubjectID:  profileID,
    ContextKey: chatagentclient.ContextPlacementIntake,
    Channel: &chatagentclient.ChannelBinding{
        Channel:   chatagentclient.ChannelSMS,
        ContactID: phoneContactID,
        ProfileID: profileID,
        Language:  "en",
    },
})

// Inbound WhatsApp message from a channel adapter
cli.IngestChannelMessage(ctx, chatagentclient.IngestChannelMessageRequest{
    SubjectID:       profileID,
    ContextKey:      chatagentclient.ContextPlacementIntake,
    CreateIfMissing: true,
    Message:         userText,
    Channel: chatagentclient.ChannelBinding{
        Channel:   chatagentclient.ChannelWhatsApp,
        ContactID: waContactID,
        ProfileID: profileID,
    },
})
```

Requires `NOTIFICATION_SERVICE_URI` on platform-chat-agent (wired in OpenTofu)
and `notification` in catalog `requestedRecipients` for chat-agent.

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
