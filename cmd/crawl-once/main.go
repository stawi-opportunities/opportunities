// crawl-once runs a single structured crawl for one or more sources and
// enqueues accepted jobs into job_ingest_queue. Intended for local ops and
// bootstrap when Trustage schedules / JWT admin aren't wired.
//
// Usage:
//
//	DATABASE_URL=... OPPORTUNITY_KINDS_DIR=... go run ./cmd/crawl-once -type remoteok
//	DATABASE_URL=... go run ./cmd/crawl-once -id d9aeoecpf2td4j9p7dj0
//	DATABASE_URL=... go run ./cmd/crawl-once -all-apis
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/pitabwire/frame/v2"
	fconfig "github.com/pitabwire/frame/v2/config"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/util"
	"github.com/rs/xid"

	"github.com/stawi-opportunities/opportunities/apps/crawler/service"
	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/crawlaccept"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/jobqueue"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

var apiTypes = map[domain.SourceType]bool{
	domain.SourceRemoteOK:  true,
	domain.SourceArbeitnow: true,
	domain.SourceJobicy:    true,
	domain.SourceTheMuse:   true,
	domain.SourceHimalayas: true,
}

func main() {
	typeFilter := flag.String("type", "", "crawl all active sources of this type")
	idFilter := flag.String("id", "", "crawl a single source id")
	allAPIs := flag.Bool("all-apis", false, "crawl all free JSON API sources")
	limit := flag.Int("limit", 0, "max sources to crawl (0 = no limit)")
	maxItems := flag.Int("max-items", 500, "stop after this many accepted items per source")
	flag.Parse()

	ctx := context.Background()
	log := util.Log(ctx)

	if *typeFilter == "" && *idFilter == "" && !*allAPIs {
		fmt.Fprintln(os.Stderr, "usage: crawl-once -type remoteok | -id <source_id> | -all-apis")
		os.Exit(2)
	}

	cfg, err := fconfig.FromEnv[fconfig.ConfigurationDefault]()
	if err != nil {
		log.WithError(err).Fatal("config parse failed")
	}
	kindsDir := os.Getenv("OPPORTUNITY_KINDS_DIR")
	if kindsDir == "" {
		kindsDir = "/tmp/opportunity-kinds"
	}
	reg, err := opportunity.LoadFromDir(kindsDir)
	if err != nil {
		log.WithError(err).Fatal("load opportunity kinds")
	}

	ctx, svc := frame.NewServiceWithContext(ctx, frame.WithConfig(&cfg), frame.WithDatastore())
	defer svc.Stop(ctx)
	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		log.Fatal("DATABASE_URL required")
	}
	db := pool.DB
	sourceRepo := repository.NewSourceRepository(db)
	queue := jobqueue.NewProducer(db, 100_000)

	client := httpx.NewClient(30*time.Second, "stawi-crawl-once/1.0 (+https://opportunities.stawi.org)")
	connReg := service.BuildRegistry(ctx, client, nil)

	var sources []*domain.Source
	if *idFilter != "" {
		s, gerr := sourceRepo.GetByID(ctx, *idFilter)
		if gerr != nil {
			log.WithError(gerr).Fatal("load source")
		}
		if s == nil {
			log.WithField("id", *idFilter).Fatal("source not found")
		}
		sources = []*domain.Source{s}
	} else {
		f := repository.ListFilter{Status: domain.SourceActive, Limit: 500}
		if *typeFilter != "" {
			f.Type = domain.SourceType(*typeFilter)
		}
		all, _, lerr := sourceRepo.ListWithFilters(ctx, f)
		if lerr != nil {
			log.WithError(lerr).Fatal("list sources")
		}
		for _, s := range all {
			if *allAPIs && !apiTypes[s.Type] {
				continue
			}
			sources = append(sources, s)
		}
	}
	if *limit > 0 && len(sources) > *limit {
		sources = sources[:*limit]
	}
	if len(sources) == 0 {
		log.Fatal("no matching sources")
	}

	var totalFound, totalEnqueued, totalRejected int
	for _, src := range sources {
		found, enq, rej, cerr := crawlSource(ctx, src, connReg, reg, queue, *maxItems)
		totalFound += found
		totalEnqueued += enq
		totalRejected += rej
		if cerr != nil {
			log.WithError(cerr).WithField("source_id", src.ID).WithField("type", src.Type).Error("crawl failed")
			continue
		}
		log.WithField("source_id", src.ID).WithField("type", src.Type).
			WithField("found", found).WithField("enqueued", enq).WithField("rejected", rej).
			Info("crawl-once complete")
	}
	fmt.Printf("done: sources=%d found=%d enqueued=%d rejected=%d\n",
		len(sources), totalFound, totalEnqueued, totalRejected)
}

func crawlSource(
	ctx context.Context,
	src *domain.Source,
	connReg *connectors.Registry,
	kinds *opportunity.Registry,
	queue *jobqueue.Store,
	maxItems int,
) (found, enqueued, rejected int, err error) {
	conn, ok := connReg.Get(src.Type)
	if !ok {
		return 0, 0, 0, fmt.Errorf("no connector for type %s", src.Type)
	}
	iter := conn.Crawl(ctx, *src)
	for iter.Next(ctx) {
		for _, opp := range iter.Items() {
			found++
			res := crawlaccept.Accept(crawlaccept.Input{
				Opp:    opp,
				Source: src,
				Kinds:  kinds,
			})
			if res.Rejected != nil {
				rejected++
				reasons := []string{}
				if res.Rejected.Reason != "" {
					reasons = append(reasons, res.Rejected.Reason)
				}
				for _, m := range res.Rejected.Missing {
					reasons = append(reasons, "missing_"+m)
				}
				_ = queue.RecordRejected(ctx, xid.New().String(), src.ID, eventsv1.VariantRejectedV1{
					VariantID:  xid.New().String(),
					SourceID:   src.ID,
					Kind:       res.Rejected.Kind,
					Title:      res.Rejected.Title,
					Reasons:    reasons,
					RejectedAt: time.Now().UTC(),
				})
				continue
			}
			if res.Accepted == nil {
				rejected++
				continue
			}
			payload, merr := json.Marshal(eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, *res.Accepted))
			if merr != nil {
				return found, enqueued, rejected, merr
			}
			if err := queue.Enqueue(ctx, jobqueue.EnqueueRequest{
				VariantID:      res.Accepted.VariantID,
				SourceID:       src.ID,
				IdempotencyKey: fmt.Sprintf("crawl-once:%s:%s", src.ID, res.Accepted.HardKey),
				Payload:        payload,
			}); err != nil {
				return found, enqueued, rejected, err
			}
			enqueued++
			if maxItems > 0 && enqueued >= maxItems {
				return found, enqueued, rejected, nil
			}
		}
	}
	if err := iter.Err(); err != nil {
		return found, enqueued, rejected, err
	}
	return found, enqueued, rejected, nil
}
