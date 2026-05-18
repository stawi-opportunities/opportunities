package v1

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/pkg/authz"
	"github.com/stawi-opportunities/opportunities/pkg/authz/stawitok"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// PairCodeKV is the minimum KV surface the pairing handlers need. The
// production wiring shares Frame's cache.RawCache with the stawitok
// issuer; tests substitute an in-memory fake.
type PairCodeKV interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// ProfileResolver maps an OIDC sub claim (profile_id) to an internal
// candidate row id. The production implementation is the
// CandidateRepository; tests substitute a fake.
type ProfileResolver interface {
	GetByProfileID(ctx context.Context, profileID string) (*domain.CandidateProfile, error)
}

// PairingDeps bundles the collaborators for the pairing handlers.
type PairingDeps struct {
	KV             PairCodeKV
	Issuer         *stawitok.Issuer
	Resolver       ProfileResolver
	CodeTTL        time.Duration // default 5m
	AccessTTL      time.Duration // default 15m
	RefreshTTL     time.Duration // default 720h (30d)
	RateAttempts   int           // max redeem attempts per IP per window (default 5)
	RateWindow     time.Duration // rate-limit window (default 1m)
}

// Defaults applied in the handler factories when zero values are
// passed in.
const (
	defaultCodeTTL    = 5 * time.Minute
	defaultAccessTTL  = 15 * time.Minute
	defaultRefreshTTL = 30 * 24 * time.Hour
	defaultRateMax    = 5
	defaultRateWindow = time.Minute
	pairKeyPrefix     = "pair:"
	rateKeyPrefix     = "pair_attempts:"
)

func (d *PairingDeps) defaults() {
	if d.CodeTTL == 0 {
		d.CodeTTL = defaultCodeTTL
	}
	if d.AccessTTL == 0 {
		d.AccessTTL = defaultAccessTTL
	}
	if d.RefreshTTL == 0 {
		d.RefreshTTL = defaultRefreshTTL
	}
	if d.RateAttempts == 0 {
		d.RateAttempts = defaultRateMax
	}
	if d.RateWindow == 0 {
		d.RateWindow = defaultRateWindow
	}
}

// PairingCreateHandler returns the handler for POST /pairings.
//
// Auth: gateway-verified JWT (web UI). The handler reads the sub
// claim via authz.ProfileIDFromJWT, resolves the candidate row, and
// mints a single-use 6-character code stored in Valkey.
func PairingCreateHandler(deps PairingDeps) http.HandlerFunc {
	deps.defaults()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)

		profileID := authz.ProfileIDFromJWT(r)
		if profileID == "" {
			writePairingError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid Bearer token")
			return
		}
		cand, err := deps.Resolver.GetByProfileID(ctx, profileID)
		if err != nil {
			log.WithError(err).Warn("pairings: resolver failed")
			writePairingError(w, http.StatusInternalServerError, "internal", "resolver failed")
			return
		}
		if cand == nil {
			writePairingError(w, http.StatusForbidden, "no_candidate", "no candidate profile for this user")
			return
		}

		code, err := randomCode()
		if err != nil {
			log.WithError(err).Error("pairings: random code")
			writePairingError(w, http.StatusInternalServerError, "internal", "code generation failed")
			return
		}
		if err := deps.KV.Set(ctx, pairKeyPrefix+code, []byte(cand.ID), deps.CodeTTL); err != nil {
			log.WithError(err).Warn("pairings: kv set")
			writePairingError(w, http.StatusInternalServerError, "internal", "kv set failed")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":       code,
			"expires_in": int(deps.CodeTTL.Seconds()),
		})
	}
}

// PairingRedeemHandler returns the handler for POST /pairings/redeem.
//
// Auth: unauthenticated. The 6-character code IS the auth.
// Body: { "code": "ABC123" }. Rate-limited by client IP.
func PairingRedeemHandler(deps PairingDeps) http.HandlerFunc {
	deps.defaults()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)

		// Rate-limit by client IP before touching the code KV — keeps
		// brute force costs flat regardless of code validity.
		if err := bumpRate(ctx, deps.KV, clientIP(r), deps.RateAttempts, deps.RateWindow); err != nil {
			writePairingError(w, http.StatusTooManyRequests, "rate_limited", err.Error())
			return
		}

		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writePairingError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
		body.Code = strings.ToUpper(strings.TrimSpace(body.Code))
		if !isValidCodeShape(body.Code) {
			writePairingError(w, http.StatusBadRequest, "bad_request", "code must be 6 alphanumeric characters")
			return
		}

		raw, found, err := deps.KV.Get(ctx, pairKeyPrefix+body.Code)
		if err != nil {
			log.WithError(err).Warn("pairings: kv get")
			writePairingError(w, http.StatusInternalServerError, "internal", "kv get failed")
			return
		}
		if !found {
			writePairingError(w, http.StatusNotFound, "invalid_code", "unknown or expired code")
			return
		}
		candidateID := strings.TrimSpace(string(raw))
		if candidateID == "" {
			writePairingError(w, http.StatusInternalServerError, "internal", "malformed pair record")
			return
		}
		// Single-use: best-effort delete. We do not fail the redeem if
		// the delete errors — the TTL will reap the row.
		if err := deps.KV.Delete(ctx, pairKeyPrefix+body.Code); err != nil {
			log.WithError(err).Warn("pairings: delete after redeem failed")
		}

		access, err := deps.Issuer.Mint(ctx, candidateID, stawitok.KindAccess, deps.AccessTTL)
		if err != nil {
			log.WithError(err).Error("pairings: mint access")
			writePairingError(w, http.StatusInternalServerError, "internal", "mint failed")
			return
		}
		refresh, err := deps.Issuer.Mint(ctx, candidateID, stawitok.KindRefresh, deps.RefreshTTL)
		if err != nil {
			log.WithError(err).Error("pairings: mint refresh")
			writePairingError(w, http.StatusInternalServerError, "internal", "mint failed")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidate_id":        candidateID,
			"access_token":        access,
			"refresh_token":       refresh,
			"access_expires_in":   int(deps.AccessTTL.Seconds()),
			"refresh_expires_in":  int(deps.RefreshTTL.Seconds()),
		})
	}
}

// PairingRefreshHandler returns the handler for POST /pairings/refresh.
//
// Auth: a Stawi refresh token in `Authorization: Bearer <refresh>`.
// Returns a fresh access token; the refresh token itself is unchanged
// (it does not rotate in v1 — a future hardening pass can rotate).
func PairingRefreshHandler(deps PairingDeps) http.HandlerFunc {
	deps.defaults()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		tok := bearerToken(r)
		if tok == "" {
			writePairingError(w, http.StatusUnauthorized, "unauthorized", "missing refresh token")
			return
		}
		info, err := deps.Issuer.Verify(ctx, tok)
		if err != nil {
			writePairingError(w, http.StatusUnauthorized, "invalid_token", "refresh token invalid")
			return
		}
		if info.Kind != stawitok.KindRefresh {
			writePairingError(w, http.StatusUnauthorized, "invalid_token", "expected refresh token")
			return
		}
		access, err := deps.Issuer.Mint(ctx, info.CandidateID, stawitok.KindAccess, deps.AccessTTL)
		if err != nil {
			log.WithError(err).Error("pairings: mint access (refresh)")
			writePairingError(w, http.StatusInternalServerError, "internal", "mint failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":      access,
			"access_expires_in": int(deps.AccessTTL.Seconds()),
		})
	}
}

// PairingRevokeHandler returns the handler for POST /pairings/revoke.
//
// Auth: gateway-verified JWT (web UI). Drops every Stawi token issued
// for the candidate by writing a tombstone. The next pair flow mints
// fresh tokens that are accepted because they're issued AFTER the
// tombstone time.
func PairingRevokeHandler(deps PairingDeps) http.HandlerFunc {
	deps.defaults()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		log := util.Log(ctx)
		profileID := authz.ProfileIDFromJWT(r)
		if profileID == "" {
			writePairingError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid Bearer token")
			return
		}
		cand, err := deps.Resolver.GetByProfileID(ctx, profileID)
		if err != nil {
			log.WithError(err).Warn("pairings: resolver failed (revoke)")
			writePairingError(w, http.StatusInternalServerError, "internal", "resolver failed")
			return
		}
		if cand == nil {
			writePairingError(w, http.StatusForbidden, "no_candidate", "no candidate profile for this user")
			return
		}
		if err := deps.Issuer.RevokeAllForCandidate(ctx, cand.ID); err != nil {
			log.WithError(err).Error("pairings: revoke all failed")
			writePairingError(w, http.StatusInternalServerError, "internal", "revoke failed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"revoked": true})
	}
}

// ── helpers ─────────────────────────────────────────────────────────

// codeAlphabet excludes vowels and ambiguous characters (0/O, 1/I/L)
// so a human can type a printed code without misreading. 28 chars × 6
// positions ≈ 4.8 × 10⁸ — enough entropy combined with the 5-attempt
// rate limit and 5-minute TTL to make brute force infeasible.
const codeAlphabet = "BCDFGHJKMNPQRSTVWXYZ23456789"

func randomCode() (string, error) {
	out := make([]byte, 6)
	max := big.NewInt(int64(len(codeAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = codeAlphabet[n.Int64()]
	}
	return string(out), nil
}

func isValidCodeShape(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := range code {
		if strings.IndexByte(codeAlphabet, code[i]) < 0 {
			return false
		}
	}
	return true
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[len("Bearer "):])
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i >= 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

// bumpRate increments a fixed-window counter for ip and returns an
// error when the request count exceeds max. The implementation is
// best-effort: a KV failure does not block the request (we'd rather
// allow the redeem than 503 the user).
func bumpRate(ctx context.Context, kv PairCodeKV, ip string, max int, window time.Duration) error {
	if ip == "" {
		return nil
	}
	key := rateKeyPrefix + ip
	raw, found, err := kv.Get(ctx, key)
	if err != nil {
		return nil //nolint:nilerr // best-effort rate limit; fail-open
	}
	count := 0
	if found {
		if n, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil {
			count = n
		}
	}
	count++
	if count > max {
		return errors.New("too many attempts; try again in a moment")
	}
	_ = kv.Set(ctx, key, []byte(strconv.Itoa(count)), window)
	return nil
}

func writePairingError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}
