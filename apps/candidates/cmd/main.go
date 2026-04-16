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

	notificationpb "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	commonpb "buf.build/gen/go/antinvestor/common/protocolbuffers/go/common/v1"
	filespb "buf.build/gen/go/antinvestor/files/protocolbuffers/go/files/v1"
	"connectrpc.com/connect"

	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/frame/security"
	securityhttp "github.com/pitabwire/frame/security/interceptors/httptor"
	"github.com/pitabwire/util"

	"stawi.jobs/apps/candidates/config"
	"stawi.jobs/apps/candidates/service/events"
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
		panic(fmt.Sprintf("config: %v", err))
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

	// Matcher
	matcher := matching.NewMatcher(jobRepo, matchRepo, candidateRepo)

	// Extractor
	var extractor *extraction.Extractor
	if cfg.OllamaURL != "" {
		extractor = extraction.NewExtractor(cfg.OllamaURL, cfg.OllamaModel)
		log.WithField("url", cfg.OllamaURL).
			WithField("model", cfg.OllamaModel).
			Info("AI extraction enabled")
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

	// Public routes (no auth required)
	mux.HandleFunc("GET /healthz", healthHandler(candidateRepo))
	mux.HandleFunc("POST /candidates/register", registerHandler(candidateRepo, extractor, svc, svcClients))
	mux.HandleFunc("POST /webhooks/inbound-email", inboundEmailHandler(candidateRepo, extractor, svc))
	mux.HandleFunc("POST /webhooks/billing", billingWebhookHandler(candidateRepo))

	// Authenticated candidate routes
	mux.Handle("GET /me", authWrap(meHandler(candidateRepo, cfg.ProfileServiceURL)))
	mux.Handle("POST /candidates/onboard", authWrap(onboardHandler(candidateRepo, extractor, svc)))
	mux.Handle("GET /candidates/profile", authWrap(getProfileHandler(candidateRepo)))
	mux.Handle("PUT /candidates/profile", authWrap(updateProfileHandler(candidateRepo)))
	mux.Handle("GET /candidates/matches", authWrap(listMatchesHandler(matchRepo)))
	mux.Handle("POST /candidates/matches/{id}/view", authWrap(viewMatchHandler(matchRepo)))
	mux.Handle("POST /jobs/{id}/save", authWrap(saveJobHandler(savedJobRepo)))
	mux.Handle("DELETE /jobs/{id}/save", authWrap(unsaveJobHandler(savedJobRepo)))
	mux.Handle("GET /saved-jobs", authWrap(listSavedJobsHandler(savedJobRepo)))

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
			defer file.Close()
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
		idStr := r.URL.Query().Get("candidate_id")
		candidateID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || candidateID <= 0 {
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
		idStr := r.PathValue("id")
		matchID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || matchID <= 0 {
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
		defer file.Close()

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

// onboardHandler creates a CandidateProfile for the authenticated user.
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

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
			return
		}

		candidate := &domain.CandidateProfile{
			ProfileID:          profileID,
			Status:             domain.CandidateActive,
			TargetJobTitle:     r.FormValue("target_job_title"),
			ExperienceLevel:    r.FormValue("experience_level"),
			JobSearchStatus:    r.FormValue("job_search_status"),
			PreferredRegions:   r.FormValue("preferred_regions"),
			PreferredTimezones: r.FormValue("preferred_timezones"),
			WantsATSReport:     r.FormValue("wants_ats_report") == "true",
			Currency:           r.FormValue("currency"),
		}

		if v := r.FormValue("salary_min"); v != "" {
			if f, err := strconv.ParseFloat(v, 32); err == nil {
				candidate.SalaryMin = float32(f)
			}
		}
		if v := r.FormValue("salary_max"); v != "" {
			if f, err := strconv.ParseFloat(v, 32); err == nil {
				candidate.SalaryMax = float32(f)
			}
		}
		if v := r.FormValue("us_work_auth"); v != "" {
			b := v == "true"
			candidate.USWorkAuth = &b
		}
		if v := r.FormValue("needs_sponsorship"); v != "" {
			b := v == "true"
			candidate.NeedsSponsorship = &b
		}

		file, header, fileErr := r.FormFile("cv")
		if fileErr == nil {
			defer file.Close()
			data, readErr := io.ReadAll(file)
			if readErr != nil {
				http.Error(w, `{"error":"failed to read CV file"}`, http.StatusBadRequest)
				return
			}
			cvText, extractErr := extraction.ExtractTextFromFile(data, header.Filename)
			if extractErr != nil {
				http.Error(w, fmt.Sprintf(`{"error":"CV extraction failed: %s"}`, extractErr.Error()), http.StatusBadRequest)
				return
			}
			candidate.CVRawText = cvText

			if extractor != nil && cvText != "" {
				fields, aiErr := extractor.ExtractCV(ctx, cvText)
				if aiErr == nil {
					applyCVFields(candidate, fields)
				}
			}
		}

		if err := candidateRepo.Create(ctx, candidate); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(candidate)
	}
}

// saveJobHandler handles POST /jobs/{id}/save — bookmarks a job for the authenticated user.
func saveJobHandler(savedJobRepo *repository.SavedJobRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := security.ClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		idStr := r.PathValue("id")
		jobID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || jobID <= 0 {
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
		idStr := r.PathValue("id")
		jobID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || jobID <= 0 {
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
		json.NewEncoder(w).Encode(map[string]any{
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
			ProfileID    string `json:"profile_id"`
			Status       string `json:"status"`
			PlanID       string `json:"plan_id"`
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
