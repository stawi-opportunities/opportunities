package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pitabwire/frame"
	fconfig "github.com/pitabwire/frame/config"
	"github.com/pitabwire/frame/datastore"
	"github.com/pitabwire/util"

	"stawi.jobs/apps/candidates/config"
	"stawi.jobs/apps/candidates/service/events"
	"stawi.jobs/pkg/domain"
	"stawi.jobs/pkg/extraction"
	"stawi.jobs/pkg/matching"
	"stawi.jobs/pkg/repository"
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

	// HTTP routes with chi
	r := chi.NewRouter()

	// Health
	r.Get("/healthz", healthHandler(candidateRepo))

	// Candidate API
	r.Post("/candidates/register", registerHandler(candidateRepo, extractor, svc))
	r.Get("/candidates/profile", getProfileHandler(candidateRepo))
	r.Put("/candidates/profile", updateProfileHandler(candidateRepo))
	r.Get("/candidates/matches", listMatchesHandler(matchRepo))
	r.Post("/candidates/matches/{id}/view", viewMatchHandler(matchRepo))

	// Admin
	r.Get("/admin/candidates", listCandidatesHandler(candidateRepo))
	r.Post("/admin/match/run", forceMatchHandler(matcher, candidateRepo))

	// Webhook
	r.Post("/webhooks/inbound-email", inboundEmailHandler(candidateRepo, extractor, svc))

	eventOpts = append(eventOpts, frame.WithHTTPHandler(r))
	svc.Init(ctx, eventOpts...)

	if runErr := svc.Run(ctx, ""); runErr != nil {
		log.WithError(runErr).Fatal("service exited with error")
	}
}

// applyCVFields maps extracted CV fields onto a CandidateProfile.
func applyCVFields(candidate *domain.CandidateProfile, fields *extraction.CVFields) {
	if fields.Name != "" {
		candidate.Name = fields.Name
	}
	if fields.Phone != "" {
		candidate.Phone = fields.Phone
	}
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

// registerHandler handles POST /candidates/register with multipart form (email + CV file).
func registerHandler(candidateRepo *repository.CandidateRepository, extractor *extraction.Extractor, svc *frame.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
			return
		}

		email := strings.TrimSpace(r.FormValue("email"))
		if email == "" {
			http.Error(w, `{"error":"email is required"}`, http.StatusBadRequest)
			return
		}

		// Check if candidate already exists.
		existing, err := candidateRepo.GetByEmail(ctx, email)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		if existing != nil {
			http.Error(w, `{"error":"candidate already registered"}`, http.StatusConflict)
			return
		}

		candidate := &domain.CandidateProfile{
			Email:  email,
			Status: domain.CandidateUnverified,
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

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(candidate)
	}
}

// getProfileHandler handles GET /candidates/profile?email=...
func getProfileHandler(candidateRepo *repository.CandidateRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := strings.TrimSpace(r.URL.Query().Get("email"))
		if email == "" {
			http.Error(w, `{"error":"email parameter is required"}`, http.StatusBadRequest)
			return
		}

		candidate, err := candidateRepo.GetByEmail(r.Context(), email)
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

// updateProfileHandler handles PUT /candidates/profile?email=...
func updateProfileHandler(candidateRepo *repository.CandidateRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		email := strings.TrimSpace(r.URL.Query().Get("email"))
		if email == "" {
			http.Error(w, `{"error":"email parameter is required"}`, http.StatusBadRequest)
			return
		}

		candidate, err := candidateRepo.GetByEmail(ctx, email)
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
		if update.Status != nil {
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
		idStr := chi.URLParam(r, "id")
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
func inboundEmailHandler(candidateRepo *repository.CandidateRepository, extractor *extraction.Extractor, svc *frame.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
			return
		}

		senderEmail := strings.TrimSpace(r.FormValue("sender"))
		if senderEmail == "" {
			http.Error(w, `{"error":"sender is required"}`, http.StatusBadRequest)
			return
		}

		// Find or create candidate.
		candidate, err := candidateRepo.GetByEmail(ctx, senderEmail)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		isNew := false
		if candidate == nil {
			candidate = &domain.CandidateProfile{
				Email:  senderEmail,
				Status: domain.CandidateUnverified,
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
				results = append(results, map[string]any{"candidate_id": c.ID, "name": c.Name, "error": matchErr.Error()})
				continue
			}
			totalMatches += n
			results = append(results, map[string]any{"candidate_id": c.ID, "name": c.Name, "matches": n})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"candidates":    len(candidates),
			"total_matches": totalMatches,
			"results":       results,
		})
	}
}
