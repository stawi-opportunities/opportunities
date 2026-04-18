# Expired-job Subscriber Notifications — Design

**Status:** Not yet implemented. Tracked in task #59.

**Problem:** When the redirect service expires a tracked link (destination
URL probed 404/410 or 3 consecutive failures), the canonical job is still
marked `active` in `stawi_jobs.canonical_jobs`, and candidates who saved
the job have no idea it's dead. We need a signal that:

1. Reflects `LinkStateExpired` on the redirect side into
   `canonical_jobs.status = 'expired'`.
2. Looks up `saved_jobs` rows pointing at the canonical job.
3. Sends each owning profile a notification ("The role you saved at
   Acme Corp is no longer accepting applications").

## Chosen path: NATS pub/sub

OpenObserve is a read-only analytics destination — we can't subscribe
to it from Go. Our cluster already runs NATS JetStream with the
`svc.files-redirect.*` stream allocated to the redirect service, and
the candidates service already has a Frame events manager wired up.

### Event shape

```
Subject: svc.files-redirect.link.expired
Stream:  svc_files_redirect (existing, workqueue retention, 1h maxAge)
Payload: LinkExpiredPayload {
  link_id:         string  // redirect-service UUID
  slug:            string  // /r/{slug} path
  affiliate_id:    string  // "canonical_job_<id>" for stawi-jobs
  destination_url: string  // the dead URL, useful for debugging
  probe_status:    int     // last HTTP status observed (0 = no response)
  expired_at:      timestamp
}
```

### Publisher (service-files/redirect)

In `RedirectHandler.maybeProbeAsync`, after the `ExpireLink` call
succeeds, publish the event via `svc.Publish(ctx, subject, payload)`.
NATS permissions already allow `pub: svc.files-redirect.>` — no new
permissions needed.

### Subscriber (stawi.jobs/candidates)

1. **NATS permissions**: extend `stawi-jobs-nats-user-creds` to allow
   `sub: svc.files-redirect.link.expired`. One-liner in
   `deployments/manifests/namespaces/stawi-jobs/common/setup_queue.yaml`.

2. **Handler**: new `pkg/pipeline/handlers/link_expired.go` that:
   - Parses `canonical_job_id` out of `affiliate_id` (strip the
     `canonical_job_` prefix, parse as int64)
   - `UpdateCanonicalFields(id, {status: "expired"})`
   - `SavedJobRepository.ListProfileIDsByCanonicalJob(id)` (new method)
   - For each profile id, call `NotificationService.Send` with a
     `job_expired` template carrying `{job_title, company_name,
     job_slug, search_url}`

3. **New repo method**:
   ```go
   func (r *SavedJobRepository) ListProfileIDsByCanonicalJob(
       ctx context.Context, canonicalJobID int64,
   ) ([]string, error) {
       var ids []string
       err := r.db(ctx, true).Model(&domain.SavedJob{}).
           Where("canonical_job_id = ?", canonicalJobID).
           Distinct("profile_id").
           Pluck("profile_id", &ids).Error
       return ids, err
   }
   ```

4. **Notification template** (lives in service-notification):
   `job_expired` with `{job_title}`, `{company_name}`,
   `{job_slug}`, localised per profile language. The template IDs
   already exist for registration / verification flows — follow the
   same pattern.

## Edge cases

- **Affiliate_id not prefixed**: non-stawi-jobs links will eventually
  flow through the same stream. The handler's `strings.HasPrefix`
  check must early-return without error so other domains can subscribe
  independently.
- **Repeated expiry**: CheckClause the update `WHERE status = 'active'`
  so re-runs don't re-flip / re-notify.
- **Idempotency on notifications**: the notification service has
  deduplication keys — pass `idempotency_key = "job_expired:{job_id}"`
  so re-processing the same NATS message doesn't double-send.

## Cost / blast radius

- Expected volume: hundreds of link expiries per week (jobs naturally
  age out). Per expiry: 1 Postgres UPDATE + N notifications where N is
  small (saved-job counts are sparse).
- Failure mode: if notification service is down, NATS retries deliver
  the message; handler returns error, message redelivers. Bounded by
  the stream's 1h maxAge — after that, we miss the notification but
  the canonical_job stays `expired`.

## Rollout order

1. Add the new repo method (stawi.jobs)
2. Add the subscriber handler + wire it in candidates (stawi.jobs)
3. Add the NATS sub permission (deployments)
4. Deploy stawi-jobs first — subscriber is idempotent when no events
   are flowing yet
5. Publish from the redirect handler (service-files)
6. Deploy service-files
7. Verify end-to-end with a manually-expired test link

## Estimated effort: 3–4 hours

Two handler files, one proto/JSON payload, three small deploy edits,
one new repo method, and a test link walk-through.
