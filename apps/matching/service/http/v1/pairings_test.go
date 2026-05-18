package v1_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	httpv1 "github.com/stawi-opportunities/opportunities/apps/matching/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/authz/stawitok"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

type pairKV struct {
	mu   sync.Mutex
	rows map[string][]byte
}

func newPairKV() *pairKV { return &pairKV{rows: map[string][]byte{}} }

func (p *pairKV) Get(_ context.Context, k string) ([]byte, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.rows[k]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}
func (p *pairKV) Set(_ context.Context, k string, v []byte, _ time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rows[k] = append([]byte(nil), v...)
	return nil
}
func (p *pairKV) Delete(_ context.Context, k string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.rows, k)
	return nil
}

type fakeResolver struct {
	byProfile map[string]*domain.CandidateProfile
}

func (f *fakeResolver) GetByProfileID(_ context.Context, profileID string) (*domain.CandidateProfile, error) {
	return f.byProfile[profileID], nil
}

func newPairingDeps(t *testing.T) (httpv1.PairingDeps, *pairKV) {
	t.Helper()
	kv := newPairKV()
	key := make([]byte, stawitok.SigningKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	is, err := stawitok.NewIssuer(kv, key)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	return httpv1.PairingDeps{
		KV:     kv,
		Issuer: is,
		Resolver: &fakeResolver{byProfile: map[string]*domain.CandidateProfile{
			"profile-1": {BaseModel: domain.BaseModel{ID: "cnd_1"}, ProfileID: "profile-1"},
		}},
	}, kv
}

func TestPairing_CreateThenRedeem_RoundTrip(t *testing.T) {
	deps, _ := newPairingDeps(t)
	createH := httpv1.PairingCreateHandler(deps)
	redeemH := httpv1.PairingRedeemHandler(deps)

	// Create
	req := httptest.NewRequest(http.MethodPost, "/pairings", nil)
	req.Header.Set("Authorization", "Bearer "+stubJWT(t, "profile-1"))
	rr := httptest.NewRecorder()
	createH(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var createBody struct {
		Code      string `json:"code"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("create decode: %v", err)
	}
	if len(createBody.Code) != 6 {
		t.Fatalf("code len = %d", len(createBody.Code))
	}

	// Redeem
	body := strings.NewReader(`{"code":"` + createBody.Code + `"}`)
	req = httptest.NewRequest(http.MethodPost, "/pairings/redeem", body)
	req.RemoteAddr = "1.2.3.4:5678"
	rr = httptest.NewRecorder()
	redeemH(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("redeem status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var redeemBody struct {
		CandidateID  string `json:"candidate_id"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &redeemBody); err != nil {
		t.Fatalf("redeem decode: %v", err)
	}
	if redeemBody.CandidateID != "cnd_1" {
		t.Fatalf("candidate_id = %q", redeemBody.CandidateID)
	}
	if redeemBody.AccessToken == "" || redeemBody.RefreshToken == "" {
		t.Fatalf("missing tokens: %+v", redeemBody)
	}

	// Verify access token works
	info, err := deps.Issuer.Verify(context.Background(), redeemBody.AccessToken)
	if err != nil {
		t.Fatalf("verify access token: %v", err)
	}
	if info.CandidateID != "cnd_1" {
		t.Fatalf("verify cid = %q", info.CandidateID)
	}

	// Second redeem with the same code must 404 (single-use).
	body = strings.NewReader(`{"code":"` + createBody.Code + `"}`)
	req = httptest.NewRequest(http.MethodPost, "/pairings/redeem", body)
	req.RemoteAddr = "9.9.9.9:1"
	rr = httptest.NewRecorder()
	redeemH(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("second redeem status = %d, want 404", rr.Code)
	}
}

func TestPairing_Create_NoJWT(t *testing.T) {
	deps, _ := newPairingDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/pairings", nil)
	rr := httptest.NewRecorder()
	httpv1.PairingCreateHandler(deps)(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestPairing_Create_NoCandidate(t *testing.T) {
	deps, _ := newPairingDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/pairings", nil)
	req.Header.Set("Authorization", "Bearer "+stubJWT(t, "unknown-profile"))
	rr := httptest.NewRecorder()
	httpv1.PairingCreateHandler(deps)(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestPairing_Redeem_BadCodeShape(t *testing.T) {
	deps, _ := newPairingDeps(t)
	body := strings.NewReader(`{"code":"abc"}`) // too short
	req := httptest.NewRequest(http.MethodPost, "/pairings/redeem", body)
	req.RemoteAddr = "1.2.3.4:1"
	rr := httptest.NewRecorder()
	httpv1.PairingRedeemHandler(deps)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestPairing_Redeem_RateLimited(t *testing.T) {
	deps, _ := newPairingDeps(t)
	deps.RateAttempts = 3
	deps.RateWindow = time.Minute
	h := httpv1.PairingRedeemHandler(deps)

	for i := 0; i < deps.RateAttempts; i++ {
		body := strings.NewReader(`{"code":"AAAAAA"}`)
		req := httptest.NewRequest(http.MethodPost, "/pairings/redeem", body)
		req.RemoteAddr = "1.2.3.4:1"
		rr := httptest.NewRecorder()
		h(rr, req)
		// Each of these returns 400 (bad code shape) or 404 (unknown code),
		// not 429 — the rate limit only kicks in on the next attempt.
		if rr.Code == http.StatusTooManyRequests {
			t.Fatalf("rate limit fired too early on attempt %d", i+1)
		}
	}
	body := strings.NewReader(`{"code":"AAAAAA"}`)
	req := httptest.NewRequest(http.MethodPost, "/pairings/redeem", body)
	req.RemoteAddr = "1.2.3.4:1"
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rr.Code)
	}
}

func TestPairing_Refresh(t *testing.T) {
	deps, _ := newPairingDeps(t)
	ctx := context.Background()
	refresh, err := deps.Issuer.Mint(ctx, "cnd_1", stawitok.KindRefresh, time.Hour)
	if err != nil {
		t.Fatalf("mint refresh: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/pairings/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+refresh)
	rr := httptest.NewRecorder()
	httpv1.PairingRefreshHandler(deps)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	info, err := deps.Issuer.Verify(ctx, body.AccessToken)
	if err != nil {
		t.Fatalf("verify access: %v", err)
	}
	if info.Kind != stawitok.KindAccess {
		t.Fatalf("kind = %q", info.Kind)
	}
}

func TestPairing_Refresh_AccessTokenRejected(t *testing.T) {
	deps, _ := newPairingDeps(t)
	access, err := deps.Issuer.Mint(context.Background(), "cnd_1", stawitok.KindAccess, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/pairings/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rr := httptest.NewRecorder()
	httpv1.PairingRefreshHandler(deps)(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestPairing_Revoke(t *testing.T) {
	deps, _ := newPairingDeps(t)
	ctx := context.Background()
	access, _ := deps.Issuer.Mint(ctx, "cnd_1", stawitok.KindAccess, time.Hour)

	req := httptest.NewRequest(http.MethodPost, "/pairings/revoke", nil)
	req.Header.Set("Authorization", "Bearer "+stubJWT(t, "profile-1"))
	rr := httptest.NewRecorder()
	httpv1.PairingRevokeHandler(deps)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, body=%s", rr.Code, rr.Body.String())
	}

	if _, err := deps.Issuer.Verify(ctx, access); err == nil {
		t.Fatal("expected token to be tombstoned after revoke-all")
	}
}

// stubJWT mirrors fakeJWT used in flags_admin_test.go: build a 3-part
// token with a sub claim, no signature verification.
func stubJWT(t *testing.T, sub string) string {
	t.Helper()
	header := "eyJhbGciOiJub25lIn0"
	body, _ := json.Marshal(map[string]any{"sub": sub})
	bodyB64 := base64URL(body)
	return header + "." + bodyB64 + ".sig"
}

func base64URL(b []byte) string {
	// RawURLEncoding without imports churn at call sites
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 0, len(b)*4/3+4)
	for i := 0; i < len(b); i += 3 {
		var n uint32
		j := 0
		for ; j < 3 && i+j < len(b); j++ {
			n |= uint32(b[i+j]) << (16 - 8*j)
		}
		for k := 0; k < j+1; k++ {
			out = append(out, alphabet[(n>>(18-6*k))&0x3F])
		}
	}
	return string(out)
}
