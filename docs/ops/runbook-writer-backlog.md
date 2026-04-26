# Runbook — Writer pub/sub backlog

**Symptom:** NATS /jsz reports num_pending on writer consumer group above 500k; writer_lag dashboard flags yellow/red.

**Cause:** R2 outage, writer pod crashloop, or sudden crawl burst above writer HPA ceiling.

**Recovery:**

1. Check R2 reachability from a writer pod: kubectl -n prod exec <writer-pod> -- curl -sI https://$R2_ENDPOINT/
2. Check writer HPA: kubectl get hpa writer -n prod
3. If HPA not at ceiling, scale manually: kubectl scale deploy writer --replicas=20 -n prod
4. kubectl logs -f -l app=writer --tail=20 | grep "parquet flushed" — verify ack rate recovers.
5. If R2 is the bottleneck (5xx in writer logs), confirm Cloudflare status page. Backlog drains automatically when R2 returns.
6. If caused by crawl burst still firing, dial down the scheduler-tick cadence: trustage update opportunities.scheduler.tick --cron "120s" temporarily.

**Success signal:** num_pending drops below 100k and stays there for 15 min.
