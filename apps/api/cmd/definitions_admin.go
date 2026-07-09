// apps/api/cmd/definitions_admin.go
//
// Admin /admin/definitions/* CRUD over the pluggable-definitions
// surface (kind YAMLs, extraction prompts, declarative connector
// specs, source seeds). Every PUT/DELETE/reload (a) writes through
// to R2 via the S3 client, (b) force-refreshes the local loader's
// cache, and (c) broadcasts opportunities.definitions.changed.v1 on
// NATS so every other reader (crawler / worker / api replicas) picks
// up the change without waiting for the 5-minute refresh tick.
//
// Validation: kind YAMLs are parsed into opportunity.Spec and run
// through Spec.Validate() so a typo in url_prefix can't crash every
// reader on the next refresh. Connector + seed YAMLs are just
// yaml-parsed for now; Plan B2 wires the real spec validator.
//
// Auth: every route is guarded by requireAdmin (Bearer token + admin
// role) — same surface as the rest of the /admin/* endpoints.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/util"
	"gopkg.in/yaml.v3"

	"github.com/stawi-opportunities/opportunities/pkg/definitions"
	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/opportunity"
)

// maxDefinitionBytes caps the body size accepted on PUT. 64 KB is
// generous for the YAMLs we expect (kind specs are ~2 KB; connector
// specs ~8 KB at the top end) while still bounding the blast radius
// of a stuck operator paste.
const maxDefinitionBytes = 64 * 1024

// definitionsLoader is the narrow slice of the loader the handlers
// use. *definitions.R2Loader satisfies this in production; tests
// substitute a definitionsMemoryAdapter wrapping MemoryLoader so the
// handler logic can run without R2.
type definitionsLoader interface {
	Get(ctx context.Context, t definitions.Type, name string) ([]byte, string, error)
	List(ctx context.Context, t definitions.Type) ([]definitions.Entry, error)
	Invalidate(ctx context.Context, t definitions.Type, name string) error
}

// definitionsS3 is the narrow slice of *s3.Client the handlers use —
// only PutObject + DeleteObject. Lifted to an interface so tests can
// substitute an in-memory fake without spinning up minio/localstack.
type definitionsS3 interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// definitionsEmitter is the broadcast hook for DefinitionsChangedV1.
// Reuses the same Emit(ctx, topic, payload) contract as
// flagEventEmitter so the existing frameEmitter satisfies it without
// any glue.
type definitionsEmitter interface {
	Emit(ctx context.Context, topic string, payload any) error
}

// definitionsAdmin bundles the dependencies the handlers need.
type definitionsAdmin struct {
	loader  definitionsLoader
	s3      definitionsS3
	bucket  string
	prefix  string
	emitter definitionsEmitter
}

// registerDefinitionsAdmin wires every /admin/definitions/* route on
// the supplied mux. Caller is responsible for constructing the
// loader, S3 client, and bucket/prefix to match where the kind YAMLs
// live in R2 — typically:
//
//	bucket = cfg.R2ContentBucket
//	prefix = "definitions"
//
// emitter may be nil; the handlers will skip the broadcast and the
// 5-minute refresh tick still propagates the edit eventually.
func registerDefinitionsAdmin(
	mux *http.ServeMux,
	loader definitionsLoader,
	s3c definitionsS3,
	bucket, prefix string,
	emitter definitionsEmitter,
) {
	a := &definitionsAdmin{
		loader:  loader,
		s3:      s3c,
		bucket:  bucket,
		prefix:  prefix,
		emitter: emitter,
	}
	mux.HandleFunc("GET /admin/definitions", requireAdmin(a.list))
	mux.HandleFunc("GET /admin/definitions/{type}/{name}", requireAdmin(a.get))
	mux.HandleFunc("PUT /admin/definitions/{type}/{name}", requireAdmin(a.put))
	mux.HandleFunc("DELETE /admin/definitions/{type}/{name}", requireAdmin(a.delete))
	mux.HandleFunc("POST /admin/definitions/reload", requireAdmin(a.reload))
}

// list handles GET /admin/definitions[?type=X]. With no query param
// every type is enumerated; with type=kind|prompt|connector|seed only
// that one slice is returned.
func (a *definitionsAdmin) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := definitions.Type(r.URL.Query().Get("type"))
	if t == "" {
		out := map[definitions.Type][]definitions.Entry{}
		for _, typ := range allDefinitionTypes() {
			es, err := a.loader.List(ctx, typ)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
			out[typ] = es
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	if !isKnownDefinitionType(t) {
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("unknown type %q", t))
		return
	}
	es, err := a.loader.List(ctx, t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"type": t, "items": es})
}

// get handles GET /admin/definitions/{type}/{name}. Returns the body
// with Content-Type=application/x-yaml and ETag=<version>.
func (a *definitionsAdmin) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := definitions.Type(r.PathValue("type"))
	name := r.PathValue("name")
	if !isKnownDefinitionType(t) {
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("unknown type %q", t))
		return
	}
	body, version, err := a.loader.Get(ctx, t, name)
	if errors.Is(err, definitions.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "definition not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("ETag", version)
	_, _ = w.Write(body)
}

// put handles PUT /admin/definitions/{type}/{name}. Validates the
// body per type, writes through to R2, invalidates the local cache,
// and broadcasts DefinitionsChangedV1.
func (a *definitionsAdmin) put(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := definitions.Type(r.PathValue("type"))
	name := r.PathValue("name")
	if !isKnownDefinitionType(t) {
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("unknown type %q", t))
		return
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid", "name required")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxDefinitionBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if len(body) > maxDefinitionBytes {
		writeError(w, http.StatusBadRequest, "too_large", "definition exceeds 64 KB")
		return
	}
	if err := validateDefinition(t, body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	key := a.objectKey(t, name)
	if _, err := a.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(a.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/x-yaml"),
	}); err != nil {
		writeError(w, http.StatusBadGateway, "r2_put_failed", err.Error())
		return
	}
	// Force local cache update + broadcast. Invalidate errors are
	// non-fatal — the 5-min tick will pick the change up.
	if err := a.loader.Invalidate(ctx, t, name); err != nil {
		util.Log(ctx).WithError(err).Warn("definitions: invalidate after put failed")
	}
	a.broadcast(ctx, t, name, "upsert")
	w.WriteHeader(http.StatusNoContent)
}

// delete handles DELETE /admin/definitions/{type}/{name}. Removes
// the R2 object, invalidates the local cache, and broadcasts a
// delete action so other readers evict their cache entries.
func (a *definitionsAdmin) delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := definitions.Type(r.PathValue("type"))
	name := r.PathValue("name")
	if !isKnownDefinitionType(t) {
		writeError(w, http.StatusBadRequest, "invalid", fmt.Sprintf("unknown type %q", t))
		return
	}
	key := a.objectKey(t, name)
	if _, err := a.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	}); err != nil {
		writeError(w, http.StatusBadGateway, "r2_delete_failed", err.Error())
		return
	}
	if err := a.loader.Invalidate(ctx, t, name); err != nil {
		util.Log(ctx).WithError(err).Warn("definitions: invalidate after delete failed")
	}
	a.broadcast(ctx, t, name, "delete")
	w.WriteHeader(http.StatusNoContent)
}

// reload handles POST /admin/definitions/reload. Broadcasts a
// wildcard reload event so every loader in the fleet re-fetches
// everything (used after a bulk seed-definitions run or when an
// operator wants to flush the cache without poking individual keys).
func (a *definitionsAdmin) reload(w http.ResponseWriter, r *http.Request) {
	a.broadcast(r.Context(), "*", "*", "reload")
	w.WriteHeader(http.StatusAccepted)
}

// broadcast emits DefinitionsChangedV1 on TopicDefinitionsChanged.
// Skips silently when no emitter is wired — the 5-min refresh tick
// still propagates the change eventually.
func (a *definitionsAdmin) broadcast(ctx context.Context, t definitions.Type, name, action string) {
	if a.emitter == nil {
		return
	}
	by := ""
	if claims := security.ClaimsFromContext(ctx); claims != nil {
		by = claims.GetProfileID()
	}
	env := eventsv1.NewEnvelope(eventsv1.TopicDefinitionsChanged, eventsv1.DefinitionsChangedV1{
		Type:      string(t),
		Name:      name,
		Version:   time.Now().UTC().Format(time.RFC3339Nano),
		Action:    action,
		ChangedAt: time.Now().UTC(),
		ChangedBy: by,
	})
	emitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.emitter.Emit(emitCtx, eventsv1.TopicDefinitionsChanged, env); err != nil {
		util.Log(ctx).WithError(err).Warn("definitions: broadcast failed")
	}
}

// objectKey builds the R2 key for a (type, name) pair.
func (a *definitionsAdmin) objectKey(t definitions.Type, name string) string {
	return a.prefix + "/" + string(t) + "/" + name + ".yaml"
}

// allDefinitionTypes is the canonical iteration order for the list
// aggregate endpoint.
func allDefinitionTypes() []definitions.Type {
	return []definitions.Type{
		definitions.TypeKind,
		definitions.TypePrompt,
		definitions.TypeConnector,
		definitions.TypeSeed,
	}
}

func isKnownDefinitionType(t definitions.Type) bool {
	switch t {
	case definitions.TypeKind, definitions.TypePrompt, definitions.TypeConnector, definitions.TypeSeed:
		return true
	}
	return false
}

// validateDefinition runs a per-type sanity check on the body so a
// malformed YAML doesn't crash every reader on the next refresh.
func validateDefinition(t definitions.Type, body []byte) error {
	if len(body) == 0 {
		return errors.New("body is empty")
	}
	switch t {
	case definitions.TypeKind:
		return validateKindYAML(body)
	case definitions.TypePrompt:
		// Prompts are free-form strings; just require non-empty.
		return nil
	case definitions.TypeConnector:
		return validateGenericYAML(body)
	case definitions.TypeSeed:
		return validateGenericYAML(body)
	default:
		return fmt.Errorf("unknown definition type %q", t)
	}
}

// validateKindYAML unmarshals the body into opportunity.Spec and runs
// Spec.Validate(). Rejects authoring errors at admission time.
func validateKindYAML(body []byte) error {
	var spec opportunity.Spec
	if err := yaml.Unmarshal(body, &spec); err != nil {
		return fmt.Errorf("yaml: %w", err)
	}
	return spec.Validate()
}

// validateGenericYAML is the placeholder for connector + seed types.
// Plan B2 swaps these for real spec validators; for B1 we only need
// to reject syntactically broken YAML.
func validateGenericYAML(body []byte) error {
	var raw map[string]any
	if err := yaml.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("yaml: %w", err)
	}
	return nil
}

// Compile-time guards: production *definitions.R2Loader satisfies
// definitionsLoader; the standard *s3.Client satisfies definitionsS3;
// flagEmitter satisfies definitionsEmitter (they share Emit signature).
var (
	_ definitionsLoader = (*definitions.R2Loader)(nil)
	_ definitionsS3     = (*s3.Client)(nil)
)

// jsonEncoderMarker keeps encoding/json imported even when the file's
// callers don't otherwise need it — preserves stability for follow-up
// changes that add JSON-encoded admin responses. Inexpensive at runtime.
var _ = json.Marshal
