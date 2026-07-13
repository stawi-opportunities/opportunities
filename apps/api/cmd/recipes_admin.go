// apps/api/cmd/recipes_admin.go
//
// Admin surface for per-source extraction recipes: inspect, install,
// dry-run/test against the live site, request AI generation, and
// roll back versions. Complements /admin/sources CRUD so operators can
// onboard a board end-to-end from the SPA without shell tools.
//
//	GET  /admin/sources/{id}/recipe
//	GET  /admin/sources/{id}/recipes
//	PUT  /admin/sources/{id}/recipe
//	POST /admin/sources/{id}/recipe/test
//	POST /admin/sources/{id}/recipe/generate
//	POST /admin/sources/{id}/recipe/rollback
//	POST /admin/sources/{id}/test
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/connectors"
	"github.com/stawi-opportunities/opportunities/pkg/connectors/httpx"
	"github.com/stawi-opportunities/opportunities/pkg/crawlaccept"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
	"github.com/stawi-opportunities/opportunities/pkg/recipe"
	"github.com/stawi-opportunities/opportunities/pkg/repository"
)

// recipeGenerateFn emits recipe.generate.v1 for the crawler. nil → 503.
type recipeGenerateFn func(ctx context.Context, sourceID string, sampleURLs []string, reason string) error

type recipesAdmin struct {
	sources    sourceAdminRepo
	recipes    *repository.RecipeRepository
	registry   *opportunity.Registry
	client     *httpx.Client
	connReg    *connectors.Registry
	passThresh float64
	generate   recipeGenerateFn
}

func registerRecipesAdmin(
	mux *http.ServeMux,
	sources sourceAdminRepo,
	recipes *repository.RecipeRepository,
	reg *opportunity.Registry,
	client *httpx.Client,
	connReg *connectors.Registry,
	generate recipeGenerateFn,
) {
	a := &recipesAdmin{
		sources:    sources,
		recipes:    recipes,
		registry:   reg,
		client:     client,
		connReg:    connReg,
		passThresh: 0.8,
		generate:   generate,
	}
	// More specific routes first.
	mux.HandleFunc("GET /admin/sources/{id}/recipe", requireAdmin(a.handleGetActive))
	mux.HandleFunc("GET /admin/sources/{id}/recipes", requireAdmin(a.handleHistory))
	mux.HandleFunc("PUT /admin/sources/{id}/recipe", requireAdmin(a.handlePut))
	mux.HandleFunc("POST /admin/sources/{id}/recipe/test", requireAdmin(a.handleTestRecipe))
	mux.HandleFunc("POST /admin/sources/{id}/recipe/generate", requireAdmin(a.handleGenerate))
	mux.HandleFunc("POST /admin/sources/{id}/recipe/rollback", requireAdmin(a.handleRollback))
	mux.HandleFunc("POST /admin/sources/{id}/test", requireAdmin(a.handleTestSource))
}

func (a *recipesAdmin) loadSource(w http.ResponseWriter, r *http.Request) *domain.Source {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "source id required")
		return nil
	}
	src, err := a.sources.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return nil
	}
	if src == nil {
		writeError(w, http.StatusNotFound, "not_found", "source not found")
		return nil
	}
	return src
}

func (a *recipesAdmin) handleGetActive(w http.ResponseWriter, r *http.Request) {
	src := a.loadSource(w, r)
	if src == nil {
		return
	}
	rec, err := a.recipes.Active(r.Context(), src.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source_id": src.ID,
		"has_recipe": rec != nil,
		"recipe":    rec,
		"listing_path": src.ListingPath,
		"needs_tuning": src.NeedsTuning,
	})
}

func (a *recipesAdmin) handleHistory(w http.ResponseWriter, r *http.Request) {
	src := a.loadSource(w, r)
	if src == nil {
		return
	}
	rows, err := a.recipes.History(r.Context(), src.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	// Decode validation reports for the UI without dumping huge recipe bodies
	// twice when not needed — still return recipe JSON for activate rollback.
	type histRow struct {
		ID               string          `json:"id"`
		Version          int             `json:"version"`
		Status           string          `json:"status"`
		PassRate         float64         `json:"pass_rate"`
		Model            string          `json:"model"`
		ValidationReport json.RawMessage `json:"validation_report,omitempty"`
		Recipe           json.RawMessage `json:"recipe"`
		CreatedAt        time.Time       `json:"created_at"`
	}
	out := make([]histRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, histRow{
			ID: row.ID, Version: row.Version, Status: row.Status,
			PassRate: row.PassRate, Model: row.Model,
			ValidationReport: row.ValidationReport, Recipe: row.Recipe,
			CreatedAt: row.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source_id": src.ID,
		"versions":  out,
		"count":     len(out),
	})
}

type putRecipeRequest struct {
	Recipe   json.RawMessage `json:"recipe"`
	Activate *bool           `json:"activate"` // default true
	Model    string          `json:"model,omitempty"`
}

func (a *recipesAdmin) handlePut(w http.ResponseWriter, r *http.Request) {
	src := a.loadSource(w, r)
	if src == nil {
		return
	}
	var req putRecipeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if len(req.Recipe) == 0 {
		writeError(w, http.StatusBadRequest, "missing_recipe", "recipe JSON required")
		return
	}
	var rec recipe.Recipe
	if err := json.Unmarshal(req.Recipe, &rec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_recipe", err.Error())
		return
	}
	rec.Normalize()
	if err := rec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_recipe", err.Error())
		return
	}
	activate := true
	if req.Activate != nil {
		activate = *req.Activate
	}
	if !activate {
		// Structural check only — do not persist.
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "validated": true, "activated": false, "recipe": rec,
		})
		return
	}
	model := req.Model
	if model == "" {
		model = "operator"
	}
	if err := a.recipes.Activate(r.Context(), src.ID, &rec, 1.0, model, map[string]any{
		"source": "admin_put", "at": time.Now().UTC(),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "activate_failed", err.Error())
		return
	}
	// Clear needs_tuning when operator installs a recipe deliberately.
	_ = a.sources.Update(r.Context(), src.ID, map[string]any{
		"needs_tuning": false, "needs_tuning_at": nil,
	})
	logAction(r, "recipe_activate", src.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "activated": true, "source_id": src.ID, "recipe": rec,
	})
}

type testRecipeRequest struct {
	Recipe  json.RawMessage `json:"recipe,omitempty"` // empty → active recipe
	Samples int             `json:"samples,omitempty"`
}

func (a *recipesAdmin) handleTestRecipe(w http.ResponseWriter, r *http.Request) {
	src := a.loadSource(w, r)
	if src == nil {
		return
	}
	var req testRecipeRequest
	_ = decodeJSON(r, &req) // body optional
	sampleN := req.Samples
	if sampleN <= 0 {
		sampleN = 3
	}
	if sampleN > 10 {
		sampleN = 10
	}

	var rec *recipe.Recipe
	if len(req.Recipe) > 0 {
		var parsed recipe.Recipe
		if err := json.Unmarshal(req.Recipe, &parsed); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_recipe", err.Error())
			return
		}
		parsed.Normalize()
		rec = &parsed
	} else {
		active, err := a.recipes.Active(r.Context(), src.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
			return
		}
		if active == nil {
			writeError(w, http.StatusNotFound, "no_recipe",
				"source has no active recipe; supply recipe in body or generate one first")
			return
		}
		rec = active
	}
	if err := rec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_recipe", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	report, err := a.dryRunRecipe(ctx, src, rec, sampleN)
	if err != nil {
		writeError(w, http.StatusBadGateway, "test_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (a *recipesAdmin) dryRunRecipe(ctx context.Context, src *domain.Source, rec *recipe.Recipe, sampleN int) (map[string]any, error) {
	fetcher := recipe.NewHTTPFetcher(a.client)
	ex := recipe.NewExecutor(rec, fetcher)

	out := map[string]any{
		"source_id":    src.ID,
		"acquisition":  rec.Acquisition,
		"threshold":    a.passThresh,
		"structural":   "ok",
		"verified":     false,
	}

	// Stage 1: list/api page execution.
	items, _, status, _, done, perr := ex.Page(ctx, *src, recipe.PageState{})
	pageSummary := map[string]any{
		"http_status": status,
		"item_count":  len(items),
		"more_pages":  !done,
	}
	if perr != nil {
		pageSummary["error"] = perr.Error()
	}
	// Preview first few items (sanitized).
	previews := make([]map[string]any, 0, 5)
	verified := 0
	for i := range items {
		v := opportunity.Verify(&items[i], src, a.registry)
		if v.OK {
			verified++
		}
		if len(previews) < 5 {
			previews = append(previews, map[string]any{
				"title":          items[i].Title,
				"issuing_entity": items[i].IssuingEntity,
				"apply_url":      items[i].ApplyURL,
				"kind":           items[i].Kind,
				"verify_ok":      v.OK,
				"missing":        v.Missing,
			})
		}
	}
	pageSummary["verify_passed"] = verified
	pageSummary["items"] = previews
	out["page"] = pageSummary

	if rec.Acquisition == "api" {
		out["pass_rate"] = 0.0
		if len(items) > 0 {
			out["pass_rate"] = float64(verified) / float64(len(items))
		}
		out["verified"] = len(items) > 0 && verified > 0
		out["gate"] = "api_page"
		return out, nil
	}

	// Stage 2: detail-sample activation gate (HTML / list recipes).
	sampleURLs, lerr := ex.ListDetailURLs(ctx, *src)
	if lerr != nil || len(sampleURLs) == 0 {
		listing := src.BaseURL
		if src.ListingPath != "" {
			listing = strings.TrimSuffix(src.BaseURL, "/") + "/" + strings.TrimPrefix(src.ListingPath, "/")
		}
		if urls, derr := recipe.DiscoverDetailURLs(ctx, fetcher, listing, sampleN); derr == nil {
			sampleURLs = urls
		}
	}
	if len(sampleURLs) > sampleN {
		sampleURLs = sampleURLs[:sampleN]
	}
	var samples []recipe.SamplePage
	fetchNotes := []string{}
	for _, u := range sampleURLs {
		body, st, ferr := fetcher.Get(ctx, u)
		if ferr != nil || st != 200 {
			fetchNotes = append(fetchNotes, fmt.Sprintf("%s status=%d err=%v", u, st, ferr))
			continue
		}
		samples = append(samples, recipe.SamplePage{URL: u, HTML: string(body)})
	}
	rep := recipe.ValidateRecipe(rec, *src, samples, a.registry)
	out["validation"] = rep
	out["pass_rate"] = rep.PassRate
	out["sample_fetch_notes"] = fetchNotes
	out["gate"] = "detail_samples"
	out["verified"] = rep.PassRate >= a.passThresh && len(items) > 0
	return out, nil
}

type generateRecipeRequest struct {
	SampleURLs []string `json:"sample_urls,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

func (a *recipesAdmin) handleGenerate(w http.ResponseWriter, r *http.Request) {
	src := a.loadSource(w, r)
	if src == nil {
		return
	}
	if a.generate == nil {
		writeError(w, http.StatusServiceUnavailable, "generate_unavailable",
			"recipe generation requires events bus + crawler with inference configured")
		return
	}
	var req generateRecipeRequest
	_ = decodeJSON(r, &req)
	reason := req.Reason
	if reason == "" {
		reason = "admin_manual"
	}
	if err := a.generate(r.Context(), src.ID, req.SampleURLs, reason); err != nil {
		writeError(w, http.StatusInternalServerError, "emit_failed", err.Error())
		return
	}
	logAction(r, "recipe_generate", src.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok": true, "queued": true, "source_id": src.ID,
		"message": "recipe.generate.v1 emitted; crawler will synthesize and activate if pass rate clears threshold",
	})
}

type rollbackRecipeRequest struct {
	Version int `json:"version"`
}

func (a *recipesAdmin) handleRollback(w http.ResponseWriter, r *http.Request) {
	src := a.loadSource(w, r)
	if src == nil {
		return
	}
	var req rollbackRecipeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	// Allow ?version= as well for shell-friendly use.
	if req.Version <= 0 {
		if v, err := strconv.Atoi(r.URL.Query().Get("version")); err == nil {
			req.Version = v
		}
	}
	if req.Version <= 0 {
		writeError(w, http.StatusBadRequest, "missing_version", "version required")
		return
	}
	if err := a.recipes.Rollback(r.Context(), src.ID, req.Version); err != nil {
		writeError(w, http.StatusBadRequest, "rollback_failed", err.Error())
		return
	}
	logAction(r, "recipe_rollback", src.ID)
	rec, _ := a.recipes.Active(r.Context(), src.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "source_id": src.ID, "version": req.Version, "recipe": rec,
	})
}

// handleTestSource dry-runs one page of extraction for the source without
// enqueueing. Prefers the active recipe; falls back to the connector registry.
func (a *recipesAdmin) handleTestSource(w http.ResponseWriter, r *http.Request) {
	src := a.loadSource(w, r)
	if src == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	rec, err := a.recipes.Active(ctx, src.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	if rec != nil {
		report, terr := a.dryRunRecipe(ctx, src, rec, 3)
		if terr != nil {
			writeError(w, http.StatusBadGateway, "test_failed", terr.Error())
			return
		}
		report["mode"] = "recipe"
		writeJSON(w, http.StatusOK, report)
		return
	}

	// Connector path.
	if a.connReg == nil {
		writeError(w, http.StatusServiceUnavailable, "no_connector",
			"no recipe and connector registry unavailable")
		return
	}
	conn, ok := a.connReg.Get(src.Type)
	if !ok {
		writeError(w, http.StatusNotFound, "no_connector",
			fmt.Sprintf("no recipe and no connector registered for type %q — install a recipe or use a known connector type", src.Type))
		return
	}
	iter := conn.Crawl(ctx, *src)
	found, accepted, rejected := 0, 0, 0
	previews := []map[string]any{}
	rejectSamples := []map[string]any{}
	for iter.Next(ctx) {
		for _, opp := range iter.Items() {
			found++
			res := crawlaccept.Accept(crawlaccept.Input{
				Opp: opp, Source: src, Kinds: a.registry,
			})
			if res.Rejected != nil {
				rejected++
				if len(rejectSamples) < 5 {
					rejectSamples = append(rejectSamples, map[string]any{
						"title":  res.Rejected.Title,
						"reason": res.Rejected.Reason,
						"detail": res.Rejected.Detail,
						"missing": res.Rejected.Missing,
					})
				}
				continue
			}
			if res.Accepted == nil {
				rejected++
				continue
			}
			accepted++
			if len(previews) < 5 {
				previews = append(previews, map[string]any{
					"title":          res.Accepted.Title,
					"issuing_entity": res.Accepted.IssuingEntity,
					"apply_url":      res.Accepted.ApplyURL,
					"kind":           res.Accepted.Kind,
					"hard_key":       res.Accepted.HardKey,
				})
			}
		}
		// One page is enough for a smoke test.
		break
	}
	if ierr := iter.Err(); ierr != nil && found == 0 {
		writeError(w, http.StatusBadGateway, "crawl_failed", ierr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":            "connector",
		"source_id":       src.ID,
		"source_type":     src.Type,
		"found":           found,
		"accepted":        accepted,
		"rejected":        rejected,
		"items":           previews,
		"rejections":      rejectSamples,
		"verified":        accepted > 0,
		"iterator_error":  nilIfEmpty(iter.Err()),
	})
}

func nilIfEmpty(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

