package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	commonpb "buf.build/gen/go/antinvestor/common/protocolbuffers/go/common/v1"
	filespb "buf.build/gen/go/antinvestor/files/protocolbuffers/go/files/v1"
	notificationpb "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/frame/security"
	securityhttp "github.com/pitabwire/frame/security/interceptors/httptor"
	"github.com/pitabwire/util"

	"stawi.jobs/apps/candidates/config"
	"stawi.jobs/apps/candidates/service/events"
	"stawi.jobs/pkg/cv"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/matching"
	"stawi.jobs/pkg/repository"
	"stawi.jobs/pkg/services"
)

func main() {
	ctx := context.Background()

	cfg, err := fconfig.FromEnv[config.CandidatesConfig]()
	if err != nil {
		util.Log(ctx).WithError(err).Fatal("candidates: config parse failed")
	}

	ctx, svc := frame.NewServiceWithContext(ctx,
		frame.WithConfig(&cfg),
		frame.WithDatastore(),
	)

	log := util.Log(ctx)

	pool := svc.DatastoreManager().GetPool(ctx, datastore.DefaultPoolName)
	dbFn := pool.DB

	// Migration
	if cfg.DoDatabaseMigrate() {
		migrationDB := dbFn(ctx, false)
		if migErr := migrationDB.AutoMigrate(
			&domain.CandidateProfile{},
			&domain.CandidateMatch{},
			&domain.CandidateApplication{},
			&domain.SavedJob{},
			&domain.RerankCache{},
			&domain.RawRef{},
		); migErr != nil {
			log.WithError(migErr).Fatal("auto-migrate failed")
		}
		log.Info("migration complete")
		return
	}

	// Repos
	candidateRepo := repository.NewCandidateRepository(dbFn)
	matchRepo := repository.NewMatchRepository(dbFn)
	jobRepo := repository.NewJobRepository(dbFn)
	savedJobRepo := repository.NewSavedJobRepository(dbFn)
	rerankCache := repository.NewRerankCacheRepository(dbFn)

	// Matcher — reranker is opt-in via RERANK_ENABLED.
	matcher := matching.NewMatcher(jobRepo, matchRepo, candidateRepo)

	// Extractor — OpenAI-compatible back-end with Ollama fallback.
	var extractor *extraction.Extractor
	infBase, infModel, infKey := extraction.ResolveInference(
		cfg.InferenceBaseURL, cfg.InferenceModel, cfg.InferenceAPIKey,
		cfg.OllamaURL, cfg.OllamaModel,
	)
	if infBase != "" {
		embBase, embModel, embKey := extraction.ResolveEmbedding(
			cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingAPIKey,
			cfg.OllamaURL, cfg.OllamaModel,
		)
		extractor = extraction.New(extraction.Config{
			BaseURL:          infBase,
			APIKey:           infKey,
			Model:            infModel,
			EmbeddingBaseURL: embBase,
			EmbeddingAPIKey:  embKey,
			EmbeddingModel:   embModel,
			RerankBaseURL:    cfg.RerankBaseURL,
			RerankAPIKey:     cfg.RerankAPIKey,
			RerankModel:      cfg.RerankModel,
		})
		log.WithField("url", infBase).WithField("model", infModel).Info("AI extraction enabled")

		if cfg.RerankEnabled && extractor.RerankerVersion() != "" {
			matcher = matcher.WithReranker(extractor, rerankCache, cfg.RerankTopK)
			log.WithField("model", extractor.RerankerVersion()).
				WithField("top_k", cfg.RerankTopK).
				Info("Cross-encoder reranking enabled")
		}
	} else {
		_ = rerankCache
	}

	// Antinvestor service clients (each is optional).
	svcClients, clientsErr := services.NewClients(ctx, &cfg, services.ClientConfig{
		NotificationURI: cfg.NotificationServiceURI,
		FileURI:         cfg.FileServiceURI,
		RedirectURI:     cfg.RedirectServiceURI,
		BillingURI:      cfg.BillingServiceURI,
		ProfileURI:      cfg.ProfileServiceURI,
	})
	if clientsErr != nil {
		log.WithError(clientsErr).Warn("some service clients failed to initialize")
	}
	if svcClients == nil {
		svcClients = &services.Clients{}
	}

	// Events
	var eventOpts []frame.Option
	if extractor != nil {
		eventOpts = append(eventOpts,
			frame.WithRegisterEvents(
				events.NewProfileCreatedHandler(candidateRepo),
				events.NewCandidateEmbeddingHandler(extractor, candidateRepo),
			),
		)
	}

	// Build auth middleware if SecurityManager is configured.
	var authMiddleware func(http.Handler) http.Handler
	if secMgr := svc.SecurityManager(); secMgr != nil {
		if authenticator := secMgr.GetAuthenticator(ctx); authenticator != nil {
			authMiddleware = func(next http.Handler) http.Handler {
				return securityhttp.AuthenticationMiddleware(next, authenticator)
			}
			log.Info("authenticated endpoints protected with JWT authentication")
		} else {
			log.Warn("SecurityManager present but no authenticator configured — endpoints are UNPROTECTED")
		}
	} else {
		log.Warn("no SecurityManager configured — endpoints are UNPROTECTED")
	}

	// HTTP routes with standard ServeMux
	mux := http.NewServeMux()

	// authWrap wraps a handler with auth middleware when configured.
	authWrap := func(h http.HandlerFunc) http.Handler {
		if authMiddleware != nil {
			return authMiddleware(h)
		}
		return h
	}

	// Billing — orchestrates checkouts through service_billing and
	// service_payment. stawi never talks to Polar.sh / DusuPay /
	// M-Pesa directly; those integrations live inside
	// service_payment's per-provider apps.
	billingClient := buildBillingClient(&cfg, svcClients)
	if billingClient == nil {
		log.Warn("billing client unwired — /billing/checkout will return 503 until BILLING_SERVICE_URI and service_billing deps are configured")
	}
	go runBillingReconciler(ctx, candidateRepo, billingClient, cfg.BillingReconcileInterval)

	// Public routes (no auth required)
	mux.HandleFunc("GET /healthz", healthHandler(candidateRepo))
	mux.HandleFunc("POST /candidates/register", registerHandler(candidateRepo, extractor, svc, svcClients))
	mux.HandleFunc("POST /webhooks/inbound-email", inboundEmailHandler(candidateRepo, extractor, svc))
	// Legacy webhook — retained for back-compat with upstream callers
	// that still POST {profile_id, status, ...} directly. New flows
	// should rely on the reconciler instead.
	mux.HandleFunc("POST /webhooks/billing", billingWebhookHandler(candidateRepo))
	// Plan catalog is public so the pricing page can render the right
	// per-country price before the user starts checkout.
	mux.HandleFunc("GET /billing/plans", plansHandler())
	// Checkout status — public because callers hit it mid-redirect
	// with a prompt_id. The prompt_id is an opaque service_payment
	// identifier; leaking status to a guesser has no material impact.
	mux.HandleFunc("GET /billing/checkout/status", checkoutStatusHandler(candidateRepo, billingClient))

	// Internal: the redirect service (service-files) calls this when
	// it flips a link to EXPIRED after the destination URL has been
	// unreachable long enough. Body: {link_id, affiliate_id}. We
	// identify the canonical job by parsing "canonical_job_<id>" out
	// of affiliate_id, mark it expired, and notify every profile that
	// saved it. Cluster-internal only — the gateway HTTPRoute doesn't
	// expose /internal/* so no auth middleware is attached.
	mux.HandleFunc("POST /internal/link-expired", linkExpiredHandler(jobRepo, savedJobRepo, svcClients))

	// Authenticated candidate routes
	mux.Handle("GET /me", authWrap(meHandler(candidateRepo, cfg.ProfileServiceURL)))
	mux.Handle("GET /me/subscription", authWrap(meSubscriptionHandler(candidateRepo, matchRepo)))
	mux.Handle("POST /billing/checkout", authWrap(checkoutHandler(candidateRepo, billingClient, &cfg)))
	mux.Handle("POST /candidates/onboard", authWrap(onboardHandler(candidateRepo, extractor, svc)))
	mux.Handle("PUT /me/cv", authWrap(uploadCVHandler(candidateRepo, extractor)))
	mux.Handle("GET /candidates/profile", authWrap(getProfileHandler(candidateRepo)))
	mux.Handle("PUT /candidates/profile", authWrap(updateProfileHandler(candidateRepo)))
	mux.Handle("GET /candidates/matches", authWrap(listMatchesHandler(matchRepo)))
	mux.Handle("POST /candidates/matches/{id}/view", authWrap(viewMatchHandler(matchRepo)))
	mux.Handle("POST /jobs/{id}/save", authWrap(saveJobHandler(savedJobRepo)))
	mux.Handle("DELETE /jobs/{id}/save", authWrap(unsaveJobHandler(savedJobRepo)))
	mux.Handle("GET /saved-jobs", authWrap(listSavedJobsHandler(savedJobRepo)))
	// CV Strength Report — returns the cached report for the caller
	// (fast path) or generates a fresh one if the stored CV version
	// differs from what was last scored (re-score loop).
	mux.Handle("GET /candidates/cv/score", authWrap(getCVScoreHandler(candidateRepo, extractor)))
	mux.Handle("POST /candidates/cv/score", authWrap(rescoreCVHandler(candidateRepo, extractor)))

	// Authenticated admin routes
	mux.Handle("GET /admin/candidates", authWrap(listCandidatesHandler(candidateRepo)))
	mux.Handle("POST /admin/match/run", authWrap(forceMatchHandler(matcher, candidateRepo)))

	eventOpts = append(eventOpts, frame.WithHTTPHandler(mux))
	svc.Init(ctx, eventOpts...)

	if runErr := svc.Run(ctx, ""); runErr != nil {
		log.WithError(runErr).Fatal("service exited with error")
	}
}

// applyCVFields maps extracted CV fields onto a CandidateProfile.
func applyCVFields(candidate *domain.CandidateProfile, fields *extraction.CVFields) {
	if fields.CurrentTitle != "" {
		candidate.CurrentTitle = fields.CurrentTitle
	}
	if fields.Seniority != "" {
		candidate.Seniority = fields.Seniority
	}
	if fields.YearsExperience > 0 {
		candidate.YearsExperience = fields.YearsExperience
	}
	if fields.Bio != "" {
		candidate.Bio = fields.Bio
	}
	if fields.Education != "" {
		candidate.Education = fields.Education
	}
	if fields.PrimaryIndustry != "" {
		candidate.Industries = fields.PrimaryIndustry
	}
	if len(fields.StrongSkills) > 0 {
		candidate.StrongSkills = strings.Join(fields.StrongSkills, ", ")
	}
	if len(fields.WorkingSkills) > 0 {
		candidate.WorkingSkills = strings.Join(fields.WorkingSkills, ", ")
	}
	if len(fields.ToolsFrameworks) > 0 {
		candidate.ToolsFrameworks = strings.Join(fields.ToolsFrameworks, ", ")
	}
	if len(fields.Certifications) > 0 {
		candidate.Certifications = strings.Join(fields.Certifications, ", ")
	}
	if len(fields.PreferredRoles) > 0 {
		candidate.PreferredRoles = strings.Join(fields.PreferredRoles, ", ")
	}
	if len(fields.Languages) > 0 {
		candidate.Languages = strings.Join(fields.Languages, ", ")
	}
	if len(fields.PreferredLocations) > 0 {
		candidate.PreferredLocations = strings.Join(fields.PreferredLocations, ", ")
	}
	if fields.RemotePreference != "" {
		candidate.RemotePreference = fields.RemotePreference
	}
	if fields.Currency != "" {
		candidate.Currency = fields.Currency
	}
	if fields.SalaryMin != "" {
		if v, err := strconv.ParseFloat(fields.SalaryMin, 32); err == nil {
			candidate.SalaryMin = float32(v)
		}
	}
	if fields.SalaryMax != "" {
		if v, err := strconv.ParseFloat(fields.SalaryMax, 32); err == nil {
			candidate.SalaryMax = float32(v)
		}
	}

	// Combine all skills into the Skills field.
	allSkills := append(fields.StrongSkills, fields.WorkingSkills...)
	allSkills = append(allSkills, fields.ToolsFrameworks...)
	if len(allSkills) > 0 {
		candidate.Skills = strings.Join(allSkills, ", ")
	}

	// Work history as JSON.
	if len(fields.WorkHistory) > 0 {
		if wh, err := json.Marshal(fields.WorkHistory); err == nil {
			candidate.WorkHistory = string(wh)
		}
	}
}

// buildEmbeddingText creates the text used for embedding from a candidate profile.
func buildEmbeddingText(c *domain.CandidateProfile) string {
	parts := []string{c.CurrentTitle, c.Skills, c.Bio, c.Industries}
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, " ")
}

// healthHandler returns candidate count as a health indicator.
func healthHandler(candidateRepo *repository.CandidateRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count, err := candidateRepo.Count(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":          "ok",
			"candidate_count": count,
		})
	}
}

// registerHandler handles POST /candidates/register with multipart form (profile_id + CV file).
// TODO(task5): Rewrite to use JWT-based registration flow.
func registerHandler(candidateRepo *repository.CandidateRepository, extractor *extraction.Extractor, svc *frame.Service, svcClients *services.Clients) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
			return
		}

		profileID := strings.TrimSpace(r.FormValue("profile_id"))
		if profileID == "" {
			http.Error(w, `{"error":"profile_id is required"}`, http.StatusBadRequest)
			return
		}

		// Check if candidate already exists.
		existing, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if existing != nil {
			http.Error(w, `{"error":"candidate already registered"}`, http.StatusConflict)
			return
		}

		candidate := &domain.CandidateProfile{
			ProfileID: profileID,
			Status:    domain.CandidateUnverified,
		}

		// Process CV file if provided.
		file, header, fileErr := r.FormFile("cv")
		if fileErr == nil {
			defer func() { _ = file.Close() }()
			data, readErr := io.ReadAll(file)
			if readErr != nil {
				http.Error(w, `{"error":"failed to read CV file"}`, http.StatusBadRequest)
				return
			}

			cvText, extractErr := extraction.ExtractTextFromFile(data, header.Filename)
			if extractErr != nil {
				http.Error(w, fmt.Sprintf(`{"error":"failed to extract CV text: %s"}`, extractErr.Error()), http.StatusBadRequest)
				return
			}
			candidate.CVRawText = cvText

			// AI extraction if available.
			if extractor != nil && cvText != "" {
				fields, aiErr := extractor.ExtractCV(ctx, cvText)
				if aiErr == nil {
					applyCVFields(candidate, fields)
				}
			}
		}

		// Upload CV to Files service if available and file was provided.
		if svcClients.Files != nil && candidate.CVRawText != "" {
			cvURL, uploadErr := uploadCVToFiles(ctx, svcClients, candidate.ProfileID, file, header)
			if uploadErr != nil {
				util.Log(ctx).WithError(uploadErr).Warn("CV upload to files service failed, continuing without")
			} else if cvURL != "" {
				candidate.CVUrl = cvURL
			}
		}

		if err := candidateRepo.Create(ctx, candidate); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		// Kick off CV Strength Report generation in the background.
		// Takes one LLM call for the fields re-extract + one for the
		// bullet rewrites — we don't block the register response on
		// them; the dashboard fetches the report on first load.
		kickoffCVScore(candidateRepo, extractor, candidate)

		// Emit events.
		evtMgr := svc.EventsManager()
		if evtMgr != nil {
			embText := buildEmbeddingText(candidate)
			if embText != "" {
				_ = evtMgr.Emit(ctx, events.CandidateEmbeddingEventName, &events.CandidateEmbeddingPayload{
					CandidateID: candidate.ID,
					Text:        embText,
				})
			}
			_ = evtMgr.Emit(ctx, events.ProfileCreatedEventName, &events.ProfileCreatedPayload{
				CandidateID: candidate.ID,
			})
		}

		// Send verification notification if Notification client is available.
		if svcClients.Notification != nil {
			if notifErr := sendRegistrationNotification(ctx, svcClients, candidate); notifErr != nil {
				util.Log(ctx).WithError(notifErr).Warn("registration notification failed")
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(candidate)
	}
}

// getProfileHandler handles GET /candidates/profile?profile_id=...
// TODO(task5): Rewrite to extract profile_id from JWT.
func getProfileHandler(candidateRepo *repository.CandidateRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profileID := strings.TrimSpace(r.URL.Query().Get("profile_id"))
		if profileID == "" {
			http.Error(w, `{"error":"profile_id parameter is required"}`, http.StatusBadRequest)
			return
		}

		candidate, err := candidateRepo.GetByProfileID(r.Context(), profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if candidate == nil {
			http.Error(w, `{"error":"candidate not found"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(candidate)
	}
}

// updateProfileHandler handles PUT /candidates/profile?profile_id=...
// TODO(task5): Rewrite to extract profile_id from JWT.
func updateProfileHandler(candidateRepo *repository.CandidateRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		profileID := strings.TrimSpace(r.URL.Query().Get("profile_id"))
		if profileID == "" {
			http.Error(w, `{"error":"profile_id parameter is required"}`, http.StatusBadRequest)
			return
		}

		candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if candidate == nil {
			http.Error(w, `{"error":"candidate not found"}`, http.StatusNotFound)
			return
		}

		var update struct {
			RemotePreference   *string  `json:"remote_preference"`
			AutoApply          *bool    `json:"auto_apply"`
			PreferredCountries *string  `json:"preferred_countries"`
			SalaryMin          *float32 `json:"salary_min"`
			SalaryMax          *float32 `json:"salary_max"`
			Status             *string  `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
			return
		}

		if update.RemotePreference != nil {
			candidate.RemotePreference = *update.RemotePreference
		}
		if update.AutoApply != nil {
			candidate.AutoApply = *update.AutoApply
		}
		if update.PreferredCountries != nil {
			candidate.PreferredCountries = *update.PreferredCountries
		}
		if update.SalaryMin != nil {
			candidate.SalaryMin = *update.SalaryMin
		}
		if update.SalaryMax != nil {
			candidate.SalaryMax = *update.SalaryMax
		}
		if update.Status != nil && isValidCandidateStatus(*update.Status) {
			candidate.Status = domain.CandidateStatus(*update.Status)
		}

		if err := candidateRepo.Update(ctx, candidate); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(candidate)
	}
}

// listMatchesHandler handles GET /candidates/matches?candidate_id=...
func listMatchesHandler(matchRepo *repository.MatchRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		candidateID := r.URL.Query().Get("candidate_id")
		if candidateID == "" {
			http.Error(w, `{"error":"valid candidate_id parameter is required"}`, http.StatusBadRequest)
			return
		}

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, parseErr := strconv.Atoi(l); parseErr == nil && parsed > 0 && parsed <= 200 {
				limit = parsed
			}
		}

		matches, err := matchRepo.ListForCandidate(r.Context(), candidateID, limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidate_id": candidateID,
			"count":        len(matches),
			"matches":      matches,
		})
	}
}

// viewMatchHandler handles POST /candidates/matches/{id}/view
func viewMatchHandler(matchRepo *repository.MatchRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		matchID := r.PathValue("id")
		if matchID == "" {
			http.Error(w, `{"error":"valid match id is required"}`, http.StatusBadRequest)
			return
		}

		if err := matchRepo.MarkViewed(r.Context(), matchID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "match_id": matchID})
	}
}

// listCandidatesHandler handles GET /admin/candidates?limit=&offset=
func listCandidatesHandler(candidateRepo *repository.CandidateRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		offset := 0
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, parseErr := strconv.Atoi(l); parseErr == nil && parsed > 0 && parsed <= 500 {
				limit = parsed
			}
		}
		if o := r.URL.Query().Get("offset"); o != "" {
			if parsed, parseErr := strconv.Atoi(o); parseErr == nil && parsed >= 0 {
				offset = parsed
			}
		}

		candidates, err := candidateRepo.ListAll(r.Context(), limit, offset)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		count, _ := candidateRepo.Count(r.Context())

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total":      count,
			"limit":      limit,
			"offset":     offset,
			"candidates": candidates,
		})
	}
}

// inboundEmailHandler handles POST /webhooks/inbound-email with multipart (sender, attachment).
// TODO(task5): Rewrite to use ProfileID-based lookup.
func inboundEmailHandler(candidateRepo *repository.CandidateRepository, extractor *extraction.Extractor, svc *frame.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
			return
		}

		profileID := strings.TrimSpace(r.FormValue("profile_id"))
		if profileID == "" {
			http.Error(w, `{"error":"profile_id is required"}`, http.StatusBadRequest)
			return
		}

		// Find or create candidate.
		candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		isNew := false
		if candidate == nil {
			candidate = &domain.CandidateProfile{
				ProfileID: profileID,
				Status:    domain.CandidateUnverified,
			}
			if createErr := candidateRepo.Create(ctx, candidate); createErr != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, createErr.Error()), http.StatusInternalServerError)
				return
			}
			isNew = true
		}

		// Process attachment.
		file, header, fileErr := r.FormFile("attachment")
		if fileErr != nil {
			http.Error(w, `{"error":"attachment is required"}`, http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()

		data, readErr := io.ReadAll(file)
		if readErr != nil {
			http.Error(w, `{"error":"failed to read attachment"}`, http.StatusBadRequest)
			return
		}

		cvText, extractErr := extraction.ExtractTextFromFile(data, header.Filename)
		if extractErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":"failed to extract CV text: %s"}`, extractErr.Error()), http.StatusBadRequest)
			return
		}
		candidate.CVRawText = cvText

		// AI extraction if available.
		if extractor != nil && cvText != "" {
			fields, aiErr := extractor.ExtractCV(ctx, cvText)
			if aiErr == nil {
				applyCVFields(candidate, fields)
			}
		}

		if err := candidateRepo.Update(ctx, candidate); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		// Re-score the updated CV in the background.
		kickoffCVScore(candidateRepo, extractor, candidate)

		// Emit events.
		evtMgr := svc.EventsManager()
		if evtMgr != nil {
			embText := buildEmbeddingText(candidate)
			if embText != "" {
				_ = evtMgr.Emit(ctx, events.CandidateEmbeddingEventName, &events.CandidateEmbeddingPayload{
					CandidateID: candidate.ID,
					Text:        embText,
				})
			}
			_ = evtMgr.Emit(ctx, events.ProfileCreatedEventName, &events.ProfileCreatedPayload{
				CandidateID: candidate.ID,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		status := http.StatusOK
		if isNew {
			status = http.StatusCreated
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(candidate)
	}
}

// meHandler returns the authenticated user's identity and candidate state.
func meHandler(candidateRepo *repository.CandidateRepository, profileServiceURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims := security.ClaimsFromContext(ctx)
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		profileID := claims.GetProfileID()
		if profileID == "" {
			http.Error(w, `{"error":"no profile_id in claims"}`, http.StatusUnauthorized)
			return
		}

		candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		roles := claims.GetRoles()

		response := map[string]any{
			"profile_id": profileID,
			"name":       "",
			"avatar_url": "",
			"email":      "",
			"roles":      roles,
			"candidate":  candidate,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}

// meSubscriptionHandler returns the candidate's current plan tier + queue
// stats. Used by the dashboard to render the right tier-specific surface.
func meSubscriptionHandler(candidateRepo *repository.CandidateRepository, matchRepo *repository.MatchRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims := security.ClaimsFromContext(ctx)
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		profileID := claims.GetProfileID()
		candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		plan := ""
		status := "none"
		queued := int64(0)
		if candidate != nil {
			plan = candidate.PlanID
			switch candidate.Subscription {
			case domain.SubscriptionPaid, domain.SubscriptionTrial:
				status = "active"
			case domain.SubscriptionCancelled:
				status = "cancelled"
			default:
				// Profile exists but billing webhook hasn't flipped us
				// to "paid" yet — treat as pending payment.
				status = "none"
			}
			// Queue depth: rough cut — total matches we haven't delivered
			// yet. Will be replaced by a status='new' filter once the
			// scoring cron writes proper match lifecycle states.
			if n, err := matchRepo.CountForCandidate(ctx, candidate.ID); err == nil {
				queued = n
			}
		}

		response := map[string]any{
			"plan":                plan,
			"status":              status,
			"queued_matches":      queued,
			"delivered_this_week": 0,
			"agent":               nil,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}

// onboardBody is the JSON shape accepted by POST /candidates/onboard.
// CV upload lives on a separate endpoint (PUT /me/cv) so the widget
// runtime's JSON fetch + single-file upload primitives cover the full
// onboarding flow — no multipart-with-text-fields request needed.
type onboardBody struct {
	Plan               string   `json:"plan"`
	TargetJobTitle     string   `json:"target_job_title"`
	ExperienceLevel    string   `json:"experience_level"`
	JobSearchStatus    string   `json:"job_search_status"`
	PreferredRegions   []string `json:"preferred_regions"`
	PreferredTimezones []string `json:"preferred_timezones"`
	PreferredLanguages []string `json:"preferred_languages"`
	JobTypes           []string `json:"job_types"`
	Country            string   `json:"country"`
	Currency           string   `json:"currency"`
	WantsATSReport     bool     `json:"wants_ats_report"`
	SalaryMin          float32  `json:"salary_min"`
	SalaryMax          float32  `json:"salary_max"`
	USWorkAuth         *bool    `json:"us_work_auth"`
	NeedsSponsorship   *bool    `json:"needs_sponsorship"`
	AgreeTerms         bool     `json:"agree_terms"`
}

// onboardHandler creates a CandidateProfile for the authenticated user.
// Accepts JSON only — the @stawi/profile runtime's fetch() can't
// emit multipart, so the CV upload moved to PUT /me/cv.
func onboardHandler(candidateRepo *repository.CandidateRepository, extractor *extraction.Extractor, svc *frame.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims := security.ClaimsFromContext(ctx)
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		profileID := claims.GetProfileID()

		existing, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if existing != nil {
			http.Error(w, `{"error":"already onboarded"}`, http.StatusConflict)
			return
		}

		var body onboardBody
		if decodeErr := json.NewDecoder(r.Body).Decode(&body); decodeErr != nil {
			http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
			return
		}

		// Plan: free is the default; any paid tier sets PlanID but
		// leaves Subscription="free" until the billing webhook flips
		// to "paid". Mirrors the pre-split behaviour exactly.
		plan := strings.TrimSpace(body.Plan)
		if plan == "" {
			plan = "free"
		}

		candidate := &domain.CandidateProfile{
			ProfileID:          profileID,
			Status:             domain.CandidateActive,
			Subscription:       domain.SubscriptionFree,
			PlanID:             plan,
			TargetJobTitle:     body.TargetJobTitle,
			ExperienceLevel:    body.ExperienceLevel,
			JobSearchStatus:    body.JobSearchStatus,
			PreferredRegions:   joinCSV(body.PreferredRegions),
			PreferredTimezones: joinCSV(body.PreferredTimezones),
			PreferredCountries: body.Country,
			Languages:          joinCSV(body.PreferredLanguages),
			PreferredRoles:     joinCSV(body.JobTypes),
			WantsATSReport:     body.WantsATSReport,
			Currency:           body.Currency,
			SalaryMin:          body.SalaryMin,
			SalaryMax:          body.SalaryMax,
			USWorkAuth:         body.USWorkAuth,
			NeedsSponsorship:   body.NeedsSponsorship,
		}

		if createErr := candidateRepo.Create(ctx, candidate); createErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, createErr.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(candidate)
	}
}

// uploadCVHandler handles PUT /me/cv — single-file multipart or raw
// body. The @stawi/profile runtime's upload() posts a File as raw
// bytes with a Content-Type header, so we read from r.Body directly;
// we also accept multipart for curl / legacy callers.
func uploadCVHandler(candidateRepo *repository.CandidateRepository, extractor *extraction.Extractor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims := security.ClaimsFromContext(ctx)
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		profileID := claims.GetProfileID()

		candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if candidate == nil {
			http.Error(w, `{"error":"candidate not found — finish onboarding first"}`, http.StatusConflict)
			return
		}

		// Accept either multipart (legacy curl) or raw body (runtime.upload).
		var (
			data     []byte
			filename string
		)
		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(ct, "multipart/"):
			if parseErr := r.ParseMultipartForm(10 << 20); parseErr != nil {
				http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
				return
			}
			file, header, fileErr := r.FormFile("cv")
			if fileErr != nil {
				http.Error(w, `{"error":"cv field is required"}`, http.StatusBadRequest)
				return
			}
			defer func() { _ = file.Close() }()
			filename = header.Filename
			data, err = io.ReadAll(io.LimitReader(file, 10<<20))
			if err != nil {
				http.Error(w, `{"error":"failed to read CV"}`, http.StatusBadRequest)
				return
			}
		default:
			// Raw-body upload. runtime.upload() sends the file as the
			// request body and X-Filename in a header (per the widget
			// contract); fall back to Content-Disposition when absent.
			filename = r.Header.Get("X-Filename")
			if filename == "" {
				filename = "cv.bin"
			}
			data, err = io.ReadAll(io.LimitReader(r.Body, 10<<20))
			if err != nil {
				http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
				return
			}
		}

		cvText, extractErr := extraction.ExtractTextFromFile(data, filename)
		if extractErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":"CV extraction failed: %s"}`, extractErr.Error()), http.StatusBadRequest)
			return
		}
		candidate.CVRawText = cvText

		if extractor != nil && cvText != "" {
			if fields, aiErr := extractor.ExtractCV(ctx, cvText); aiErr == nil {
				applyCVFields(candidate, fields)
			}
		}

		if updateErr := candidateRepo.Update(ctx, candidate); updateErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, updateErr.Error()), http.StatusInternalServerError)
			return
		}

		// Score the CV in the background so the dashboard has the
		// report ready on next load. Same behaviour as the pre-split
		// onboarding handler.
		kickoffCVScore(candidateRepo, extractor, candidate)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"cv_length": len(cvText),
			"candidate": candidate,
		})
	}
}

// joinCSV serialises a string slice into the comma-separated format
// the CandidateProfile text columns use. The matching engine parses
// on read; this mirrors the prior behaviour of accepting JSON-encoded
// array strings from the multipart form.
func joinCSV(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x != "" {
			out = append(out, x)
		}
	}
	return strings.Join(out, ",")
}

// saveJobHandler handles POST /jobs/{id}/save — bookmarks a job for the authenticated user.
func saveJobHandler(savedJobRepo *repository.SavedJobRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := security.ClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		jobID := r.PathValue("id")
		if jobID == "" {
			http.Error(w, `{"error":"valid job id required"}`, http.StatusBadRequest)
			return
		}

		if err := savedJobRepo.Save(r.Context(), claims.GetProfileID(), jobID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}
}

// unsaveJobHandler handles DELETE /jobs/{id}/save — removes a saved job.
func unsaveJobHandler(savedJobRepo *repository.SavedJobRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := security.ClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		jobID := r.PathValue("id")
		if jobID == "" {
			http.Error(w, `{"error":"valid job id required"}`, http.StatusBadRequest)
			return
		}

		if err := savedJobRepo.Delete(r.Context(), claims.GetProfileID(), jobID); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}
}

// listSavedJobsHandler handles GET /saved-jobs — returns saved jobs for the authenticated user.
func listSavedJobsHandler(savedJobRepo *repository.SavedJobRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := security.ClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
				limit = parsed
			}
		}

		saved, err := savedJobRepo.ListForProfile(r.Context(), claims.GetProfileID(), limit)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": len(saved),
			"saved": saved,
		})
	}
}

// isValidCandidateStatus checks if a status string is a valid CandidateStatus enum.
func isValidCandidateStatus(s string) bool {
	switch domain.CandidateStatus(s) {
	case domain.CandidateUnverified, domain.CandidateActive, domain.CandidatePaused, domain.CandidateHired:
		return true
	default:
		return false
	}
}

// forceMatchHandler runs matching for all active candidates.
func forceMatchHandler(matcher *matching.Matcher, candidateRepo *repository.CandidateRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		candidates, err := candidateRepo.ListActive(ctx, 1000)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		totalMatches := 0
		results := make([]map[string]any, 0, len(candidates))
		for _, c := range candidates {
			n, matchErr := matcher.MatchCandidateToJobs(ctx, c)
			if matchErr != nil {
				results = append(results, map[string]any{"candidate_id": c.ID, "profile_id": c.ProfileID, "error": matchErr.Error()})
				continue
			}
			totalMatches += n
			results = append(results, map[string]any{"candidate_id": c.ID, "profile_id": c.ProfileID, "matches": n})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates":    len(candidates),
			"total_matches": totalMatches,
			"results":       results,
		})
	}
}

// uploadCVToFiles uploads a CV file to the Files service via streaming RPC
// and returns the content URI. The file multipart.File may be nil if already
// consumed; in that case the function returns early.
func uploadCVToFiles(
	ctx context.Context,
	clients *services.Clients,
	profileID string,
	file multipart.File,
	header *multipart.FileHeader,
) (string, error) {
	if clients.Files == nil || file == nil || header == nil {
		return "", nil
	}

	// Reset file reader to beginning.
	if seeker, ok := file.(io.Seeker); ok {
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return "", fmt.Errorf("seek CV file: %w", err)
		}
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read CV file for upload: %w", err)
	}

	stream := clients.Files.UploadContent(ctx)

	// Send metadata first.
	if err := stream.Send(&filespb.UploadContentRequest{
		Data: &filespb.UploadContentRequest_Metadata{
			Metadata: &filespb.UploadMetadata{
				ContentType: header.Header.Get("Content-Type"),
				Filename:    header.Filename,
				TotalSize:   int64(len(data)),
			},
		},
	}); err != nil {
		return "", fmt.Errorf("upload metadata: %w", err)
	}

	// Send file content in chunks.
	const chunkSize = 64 * 1024
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&filespb.UploadContentRequest{
			Data: &filespb.UploadContentRequest_Chunk{
				Chunk: data[offset:end],
			},
		}); err != nil {
			return "", fmt.Errorf("upload chunk: %w", err)
		}
	}

	resp, err := stream.CloseAndReceive()
	if err != nil {
		return "", fmt.Errorf("upload close: %w", err)
	}

	return resp.Msg.GetContentUri(), nil
}

// sendRegistrationNotification sends a verification email notification
// for a newly registered candidate via the Notification service.
func sendRegistrationNotification(
	ctx context.Context,
	clients *services.Clients,
	candidate *domain.CandidateProfile,
) error {
	if clients.Notification == nil {
		return nil
	}

	stream, err := clients.Notification.Send(ctx, connect.NewRequest(&notificationpb.SendRequest{
		Data: []*notificationpb.Notification{
			{
				Recipient: &commonpb.ContactLink{
					ProfileId: candidate.ProfileID,
				},
				Type:     "email",
				Template: "candidate_registration_verification",
				OutBound: true,
			},
		},
	}))
	if err != nil {
		return fmt.Errorf("notification send: %w", err)
	}

	// Drain the response stream.
	for stream.Receive() {
		// Response acknowledged.
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("notification stream: %w", err)
	}

	return nil
}

// billingWebhookHandler handles POST /webhooks/billing — payment status callbacks.
// When billing confirms payment, the candidate's subscription and auto_apply are updated.
func billingWebhookHandler(candidateRepo *repository.CandidateRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var payload struct {
			ProfileID      string `json:"profile_id"`
			Status         string `json:"status"`
			PlanID         string `json:"plan_id"`
			SubscriptionID string `json:"subscription_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
			return
		}

		if payload.ProfileID == "" {
			http.Error(w, `{"error":"profile_id is required"}`, http.StatusBadRequest)
			return
		}

		candidate, err := candidateRepo.GetByProfileID(ctx, payload.ProfileID)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if candidate == nil {
			http.Error(w, `{"error":"candidate not found"}`, http.StatusNotFound)
			return
		}

		switch payload.Status {
		case "paid", "active":
			candidate.Subscription = domain.SubscriptionPaid
			candidate.AutoApply = true
			if payload.SubscriptionID != "" {
				candidate.SubscriptionID = payload.SubscriptionID
			}
			if payload.PlanID != "" {
				candidate.PlanID = payload.PlanID
			}
		case "cancelled", "expired":
			candidate.Subscription = domain.SubscriptionCancelled
			candidate.AutoApply = false
		case "trial":
			candidate.Subscription = domain.SubscriptionTrial
		default:
			http.Error(w, `{"error":"unknown status"}`, http.StatusBadRequest)
			return
		}

		if err := candidateRepo.Update(ctx, candidate); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "subscription": candidate.Subscription})
	}
}

// linkExpiredHandler reacts to a redirect-service link.expired signal.
// The redirect service calls this synchronously over the cluster
// network when its inline probe has decided a destination URL is
// permanently gone. We do three things here:
//
//  1. Parse the canonical_job_id out of affiliate_id — stawi-jobs
//     encodes "canonical_job_<id>" at publish time (see
//     pkg/pipeline/handlers/publish.go), so anything else is some
//     other domain's link and we ignore it silently with a 204.
//  2. Flip canonical_jobs.status = 'expired' (idempotent — WHERE
//     status = 'active' gates the update, so repeated calls from a
//     retrying redirect service don't double-process).
//  3. Fan out a "job_expired" notification to every profile that
//     bookmarked the job, using the existing NotificationService
//     pipeline. Each notification carries an idempotency key tied to
//     the canonical job so duplicate deliveries collapse upstream.
//
// Best-effort throughout — a failure in notification delivery
// shouldn't bubble back to the redirect service and revert its
// EXPIRED state change. We log and continue.
func linkExpiredHandler(
	jobRepo *repository.JobRepository,
	savedJobRepo *repository.SavedJobRepository,
	clients *services.Clients,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		log := util.Log(ctx)

		var body struct {
			LinkID      string `json:"link_id"`
			AffiliateID string `json:"affiliate_id"`
			Slug        string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}

		canonicalJobID, ok := parseCanonicalJobAffiliate(body.AffiliateID)
		if !ok {
			// Not ours — could be a candidate affiliate link, a
			// partner integration, anything. Ack 204 so the redirect
			// service doesn't retry.
			log.WithField("affiliate_id", body.AffiliateID).
				Debug("link-expired: affiliate not a canonical job, ignoring")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		log = log.WithField("canonical_job_id", canonicalJobID).
			WithField("link_id", body.LinkID)

		// Step 1: flip canonical status, guarded on status='active'
		// so retries don't re-trigger the notification fan-out.
		rows, err := jobRepo.MarkExpiredIfActive(ctx, canonicalJobID)
		if err != nil {
			log.WithError(err).Error("link-expired: mark expired failed")
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if rows == 0 {
			// Already expired or missing — no-op for idempotency.
			log.Debug("link-expired: canonical already expired or missing, skipping notifications")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Step 2: fan out notifications to saved-job subscribers.
		// Non-fatal — we always ack the redirect service.
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			notifyExpiredJobSubscribers(bgCtx, jobRepo, savedJobRepo, clients, canonicalJobID)
		}()

		w.WriteHeader(http.StatusNoContent)
	}
}

// parseCanonicalJobAffiliate extracts the canonical job id from an
// affiliate_id of the form "canonical_job_<xid>". Returns (id, true)
// on match, ("", false) otherwise.
func parseCanonicalJobAffiliate(affiliateID string) (string, bool) {
	const prefix = "canonical_job_"
	if !strings.HasPrefix(affiliateID, prefix) {
		return "", false
	}
	idStr := affiliateID[len(prefix):]
	if idStr == "" {
		return "", false
	}
	return idStr, true
}

// notifyExpiredJobSubscribers loads the job (for template variables)
// and the profile ids that saved it, then fires one notification per
// profile. Per-profile failures are logged but don't abort the batch —
// one muted inbox shouldn't block the other subscribers.
func notifyExpiredJobSubscribers(
	ctx context.Context,
	jobRepo *repository.JobRepository,
	savedJobRepo *repository.SavedJobRepository,
	clients *services.Clients,
	canonicalJobID string,
) {
	log := util.Log(ctx).WithField("canonical_job_id", canonicalJobID)
	if clients == nil || clients.Notification == nil {
		log.Debug("notify-expired: notification client not configured, skipping")
		return
	}

	job, err := jobRepo.GetCanonicalByIDAnyStatus(ctx, canonicalJobID)
	if err != nil {
		log.WithError(err).Warn("notify-expired: load canonical failed")
		return
	}
	if job == nil {
		return
	}

	profileIDs, err := savedJobRepo.ListProfileIDsByCanonicalJob(ctx, canonicalJobID)
	if err != nil {
		log.WithError(err).Warn("notify-expired: list saved subscribers failed")
		return
	}
	if len(profileIDs) == 0 {
		return
	}

	log.WithField("subscribers", len(profileIDs)).Info("notify-expired: fanning out")
	for _, pid := range profileIDs {
		payload, payloadErr := structpb.NewStruct(map[string]any{
			"job_title":    job.Title,
			"company_name": job.Company,
			"job_slug":     job.Slug,
			"category":     job.Category,
			"search_url":   "https://jobs.stawi.org/search/",
		})
		if payloadErr != nil {
			log.WithError(payloadErr).WithField("profile_id", pid).
				Warn("notify-expired: build payload struct failed")
			continue
		}

		stream, sendErr := clients.Notification.Send(ctx, connect.NewRequest(&notificationpb.SendRequest{
			Data: []*notificationpb.Notification{
				{
					// Deterministic Id collapses duplicate deliveries on the
					// notification service side — if the redirect service
					// somehow retries this endpoint after we've already
					// kicked off a fan-out, the second call upserts the
					// same row instead of double-sending.
					Id:        fmt.Sprintf("job_expired:%s:%s", canonicalJobID, pid),
					Recipient: &commonpb.ContactLink{ProfileId: pid},
					Type:      "email",
					Template:  "job_expired",
					OutBound:  true,
					Payload:   payload,
				},
			},
		}))
		if sendErr != nil {
			log.WithError(sendErr).WithField("profile_id", pid).
				Warn("notify-expired: send failed")
			continue
		}
		for stream.Receive() {
		}
		if streamErr := stream.Err(); streamErr != nil {
			log.WithError(streamErr).WithField("profile_id", pid).
				Warn("notify-expired: stream failed")
		}
	}
}

// ── CV Strength Report handlers ─────────────────────────────────────

// getCVScoreHandler returns the stored CV Strength Report for the
// caller. Fast path — no LLM, no scoring — so the dashboard renders
// instantly. If the stored report is stale (CV text changed since
// last scoring), the handler also kicks off a background rescore so
// the next request reflects the new CV.
func getCVScoreHandler(candidateRepo *repository.CandidateRepository, extractor *extraction.Extractor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims := security.ClaimsFromContext(ctx)
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		profileID := claims.GetProfileID()
		if profileID == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil || candidate == nil {
			http.Error(w, `{"error":"candidate not found"}`, http.StatusNotFound)
			return
		}

		// Serve the cached report when it matches the current CV.
		if candidate.CVReportJSON != "" && candidate.CVReportJSON != "{}" {
			staleVersion := candidate.CVScoredVersion != cv.VersionHashText(candidate.CVRawText)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-CV-Report-Stale", boolStr(staleVersion))
			_, _ = w.Write([]byte(candidate.CVReportJSON))
			// Fire-and-forget rescore when stale so the next hit
			// serves a fresh report.
			if staleVersion {
				go func() {
					bgCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					_, _ = scoreAndPersistCV(bgCtx, candidateRepo, extractor, candidate)
				}()
			}
			return
		}

		// No cached report → generate inline (blocks until done).
		report, scoreErr := scoreAndPersistCV(ctx, candidateRepo, extractor, candidate)
		if scoreErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, scoreErr.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
	}
}

// rescoreCVHandler forces a fresh scoring round. Used by the frontend
// after the user applies a fix — the loop is "apply → rescore →
// show improvement". Returns the freshly-generated report.
func rescoreCVHandler(candidateRepo *repository.CandidateRepository, extractor *extraction.Extractor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims := security.ClaimsFromContext(ctx)
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		profileID := claims.GetProfileID()
		if profileID == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		candidate, err := candidateRepo.GetByProfileID(ctx, profileID)
		if err != nil || candidate == nil {
			http.Error(w, `{"error":"candidate not found"}`, http.StatusNotFound)
			return
		}

		report, scoreErr := scoreAndPersistCV(ctx, candidateRepo, extractor, candidate)
		if scoreErr != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, scoreErr.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
	}
}

// scoreAndPersistCV runs the full pipeline (score → rewrite → persist)
// and returns the generated report. Used by the two HTTP handlers
// above and also by the post-upload auto-trigger that lives inline in
// the register / onboard / inbound-email handlers.
//
// Returns (nil, nil) when the candidate has no CV text yet — that's
// not an error condition, just a signal that there's nothing to
// score. Callers should check for nil and skip.
func scoreAndPersistCV(
	ctx context.Context,
	candidateRepo *repository.CandidateRepository,
	extractor *extraction.Extractor,
	candidate *domain.CandidateProfile,
) (*cv.CVStrengthReport, error) {
	if strings.TrimSpace(candidate.CVRawText) == "" {
		return nil, nil
	}

	// Re-extract fields from the stored text. This costs one LLM
	// call but keeps the scorer honest even after the user has
	// hand-edited their CandidateProfile — structured extractor
	// output is the input contract for the scorer.
	var fields *extraction.CVFields
	if extractor != nil {
		f, err := extractor.ExtractCV(ctx, candidate.CVRawText)
		if err == nil {
			fields = f
		}
	}

	// Role target — prefer the explicit onboarding field, fall back
	// to the current title. Either way the scorer copes with empty
	// by returning the "general" family.
	target := candidate.TargetJobTitle
	if target == "" {
		target = candidate.CurrentTitle
	}

	scorer := cv.NewScorer(extractor) // Extractor's Embed satisfies the Embedder interface
	report := scorer.Score(ctx, candidate.CVRawText, fields, target)

	// Append LLM-driven rewrites as a best-effort second pass.
	cv.AttachRewrites(ctx, extractor, report, fields)

	// Persist the cached report + components on the candidate row.
	blob, marshalErr := json.Marshal(report)
	if marshalErr == nil {
		now := time.Now().UTC()
		candidate.CVScore = report.OverallScore
		candidate.CVReportJSON = string(blob)
		candidate.CVScoredAt = &now
		candidate.CVScoredVersion = report.CVVersion
		if err := candidateRepo.Update(ctx, candidate); err != nil {
			// Non-fatal — we still return the report so the caller
			// can render it; persistence will retry next time.
			util.Log(ctx).WithError(err).
				WithField("profile_id", candidate.ProfileID).
				Warn("cv-score: persist failed")
		}
	}

	return report, nil
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// kickoffCVScore fires the CV Strength Report pipeline in the
// background so upload/register/onboard responses stay fast. The
// scoring pass re-extracts CV fields (one LLM call), computes all
// five dimensions (including an embedding call for role fit), and
// generates before/after rewrites for the 5 weakest bullets (one
// more LLM call). On the cluster that's ~3-5 seconds end-to-end —
// no reason to block the user on it when the result is consumed by
// the dashboard on the next page load.
func kickoffCVScore(
	candidateRepo *repository.CandidateRepository,
	extractor *extraction.Extractor,
	candidate *domain.CandidateProfile,
) {
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if _, err := scoreAndPersistCV(bgCtx, candidateRepo, extractor, candidate); err != nil {
			util.Log(bgCtx).WithError(err).
				WithField("profile_id", candidate.ProfileID).
				Warn("cv-score: background kickoff failed")
		}
	}()
}
