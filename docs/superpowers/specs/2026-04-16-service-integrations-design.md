# Service Integrations — Design Spec

**Date**: 2026-04-16
**Goal**: Wire 5 antinvestor services (notification, files, redirect, billing, profile) into the candidates app via Connect RPC clients. Enables match emails, CV storage, tracked links, subscriptions, and profile lookup.

---

## 1. Services & Roles

| Service | Cluster Endpoint | Role | Key Methods |
|---|---|---|---|
| Notification | `service-notification.notifications.svc:80` | Send emails (verification, matches, digest) | `Send` |
| Files | `service-files.files.svc:80` | Store CV files, download URLs | `UploadContent`, `GetSignedDownloadUrl` |
| Redirect | `service-redirect.files.svc:80` | Tracked apply links (no contacts in URLs) | `CreateLink`, `GetLinkStats` |
| Billing | `service-payment.payments.svc:80` | Subscription management | `CreateSubscription`, `Status` |
| Profile | `service-profile.profile.svc:80` | Lookup by email/phone (read-only) | `GetByContact` |

## 2. Client Package

```
pkg/services/clients.go
```

Single struct holding all 5 clients. Created via `connection.NewServiceClient()` from `antinvestor/common`. Each client is optional — nil if URI not configured.

Config env vars on candidates app:
```
NOTIFICATION_SERVICE_URI
FILE_SERVICE_URI
REDIRECT_SERVICE_URI
BILLING_SERVICE_URI
PROFILE_SERVICE_URI
```

## 3. Integration Points

### Registration (`POST /candidates/register`)
1. **Files**: Upload CV → get file ID → store as `cv_url`
2. **Profile**: Lookup by email → store `profile_id` if found
3. **Notification**: Send verification email

### Match Delivery (after matching)
1. **Redirect**: For each match, `CreateLink(job.ApplyURL)` → tracked short URL (no contacts in URL, only anonymous candidate_id as affiliate_id)
2. **Notification**: Send match email with tracked links

### Subscription (`POST /candidates/subscribe`)
1. **Billing**: Create subscription for candidate
2. Store billing reference on candidate profile
3. Webhook callback updates `subscription = "paid"`

### Profile Lookup (optional enrichment)
1. **Profile**: `GetByContact(email)` → get antinvestor profile_id
2. Store on candidate for cross-referencing
3. Read-only — no identity creation or management

## 4. Tracked Links (Redirect Service)

All outbound links to candidates MUST go through redirect service:
- Apply URLs in match emails
- Profile view links
- Any link in any communication

Link creation:
```go
redirectClient.CreateLink(ctx, &redirectv1.CreateLinkRequest{
    Data: &redirectv1.Link{
        DestinationUrl: job.ApplyURL,
        Campaign:       "match_email",
        AffiliateId:    fmt.Sprintf("candidate_%d", candidateID), // anonymous, no contacts
    },
})
```

Never include email, name, or phone in redirect URLs.

## 5. Notification Templates

Three email types:
1. **Verification**: "Here's what we understood from your CV..."
2. **Match notification**: "We found {N} jobs matching your profile" + tracked apply links
3. **Weekly digest** (paid users): "This week: {N} matches, {N} applications"

## 6. Billing Integration

Subscription lifecycle:
```
Free → candidate clicks subscribe
  → Billing.CreateSubscription(plan, candidate reference)
  → Redirect to payment page
  → Payment webhook: POST /webhooks/billing
  → Update candidate.subscription = "paid", candidate.auto_apply = true
  → Cancel webhook: subscription = "cancelled", auto_apply = false
```

## 7. Deployment Config

Add to candidates HelmRelease env:
```yaml
- name: NOTIFICATION_SERVICE_URI
  value: "http://service-notification.notifications.svc:80"
- name: FILE_SERVICE_URI
  value: "http://service-files.files.svc:80"
- name: REDIRECT_SERVICE_URI
  value: "http://service-redirect.files.svc:80"
- name: BILLING_SERVICE_URI
  value: "http://service-payment.payments.svc:80"
- name: PROFILE_SERVICE_URI
  value: "http://service-profile.profile.svc:80"
```

## 8. Graceful Degradation

If a service URI is empty or service is down:
- **Notification down**: Matches stored, emails queued for later (via NATS event retry)
- **Files down**: CV stored as raw text in DB (current behavior, works)
- **Redirect down**: Use raw apply URL (no tracking, but functional)
- **Billing down**: Subscription creation fails, candidate stays free
- **Profile down**: No profile lookup, candidate works standalone
