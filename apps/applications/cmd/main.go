package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/pitabwire/frame/v2"
	"github.com/pitabwire/frame/v2/datastore"
	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/util"

	appsconfig "github.com/stawi-opportunities/opportunities/apps/applications/config"
	v1 "github.com/stawi-opportunities/opportunities/apps/applications/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/applications"
	"github.com/stawi-opportunities/opportunities/pkg/httpmw"
)

func main() {
	ctx := context.Background()
	log := util.Log(ctx)

	cfg := appsconfig.Config{}
	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)
	defer svc.Stop(ctx)

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	if pool == nil {
		log.Fatal("applications: DATABASE_URL required")
	}
	gdb := pool.DB(ctx, false)
	sqlDB, err := gdb.DB()
	if err != nil {
		log.WithError(err).Fatal("applications: open *sql.DB")
	}

	store := applications.NewStore(sqlDB)
	events := applications.NewEventLog(sqlDB)
	notes := applications.NewNotesStore(sqlDB)
	reminders := applications.NewRemindersStore(sqlDB)
	attachments := applications.NewAttachmentsStore(sqlDB)
	idem := applications.NewIdempotencyStore(sqlDB, time.Duration(cfg.IdempotencyTTLHours)*time.Hour)

	var blobs applications.BlobStore = applications.NewMemoryBlobStore()
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" && cfg.R2SecretAccessKey != "" && cfg.R2AttachmentsBucket != "" {
		r2, err := applications.NewR2BlobStore(applications.R2BlobConfig{
			AccountID:       cfg.R2AccountID,
			AccessKeyID:     cfg.R2AccessKeyID,
			SecretAccessKey: cfg.R2SecretAccessKey,
			Bucket:          cfg.R2AttachmentsBucket,
		})
		if err != nil {
			log.WithError(err).Fatal("applications: r2 blob store init failed")
		}
		blobs = r2
		log.WithField("bucket", cfg.R2AttachmentsBucket).Info("applications: R2 blob store enabled")
	} else {
		log.Warn("applications: R2 not configured — using in-memory blob store (attachments will not survive restarts)")
	}

	var authenticator security.Authenticator
	if secMgr := svc.SecurityManager(); secMgr != nil {
		authenticator = secMgr.GetAuthenticator(ctx)
	}
	if authenticator != nil {
		log.Info("applications: /api/me/* protected with JWT authentication")
	} else {
		log.Warn("applications: no JWT authenticator — /api/me/* accepts X-Candidate-ID only (dev/test)")
	}
	authMW := httpmw.NewCandidateAuth(authenticator)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"enabled": cfg.ApplicationsEnabled,
		})
	})

	if cfg.ApplicationsEnabled {
		v1.Mount(mux, &v1.Deps{
			Store:            store,
			EventLog:         events,
			NotesStore:       notes,
			RemindersStore:   reminders,
			AttachmentsStore: attachments,
			BlobStore:        blobs,
			Idempotency:      idem,
			Auth:             authMW,
		})
		log.Info("applications: HTTP routes mounted")
	} else {
		log.Info("applications: APPLICATIONS_ENABLED=false; only healthz exposed")
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.WithField("addr", cfg.HTTPAddr).Info("applications: starting http server")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.WithError(err).Error("applications: http server crashed")
		os.Exit(1)
	}
}
