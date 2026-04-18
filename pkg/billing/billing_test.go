package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── geo ─────────────────────────────────────────────────────────────

func TestIsAfrica(t *testing.T) {
	cases := []struct {
		country string
		want    bool
	}{
		{"KE", true}, {"ke", true}, {"NG", true}, {"ZA", true},
		{"EG", true}, {"MA", true},
		{"US", false}, {"GB", false}, {"IN", false}, {"", false},
		{"XX", false}, {"  KE  ", true},
	}
	for _, c := range cases {
		if got := IsAfrica(c.country); got != c.want {
			t.Errorf("IsAfrica(%q)=%v want %v", c.country, got, c.want)
		}
	}
}

func TestMobileMoneyCurrency(t *testing.T) {
	cur, ok := MobileMoneyCurrency("KE")
	if !ok || cur != "KES" {
		t.Errorf("KE -> %q ok=%v, want KES true", cur, ok)
	}
	if _, ok := MobileMoneyCurrency("US"); ok {
		t.Error("US should not map to mobile-money currency")
	}
	if _, ok := MobileMoneyCurrency(""); ok {
		t.Error("empty country must not map")
	}
}

// ── catalog ────────────────────────────────────────────────────────

func TestLookupPlan(t *testing.T) {
	plan, err := LookupPlan("pro")
	if err != nil {
		t.Fatal(err)
	}
	if plan.ID != PlanPro || plan.USDCents != 5000 {
		t.Errorf("pro lookup: got %+v", plan)
	}
	if _, err := LookupPlan("PRO"); err != nil {
		t.Error("lookup must be case-insensitive")
	}
	if _, err := LookupPlan("free"); err == nil {
		t.Error("free should not resolve via LookupPlan")
	}
	if _, err := LookupPlan("nonsense"); err == nil {
		t.Error("unknown plan must error")
	}
}

func TestPlanPriceFor(t *testing.T) {
	starter := Catalog[PlanStarter]
	amt, cur := starter.PriceFor("KE")
	if cur != "KES" {
		t.Errorf("KE starter currency=%s, want KES", cur)
	}
	if amt != starter.LocalPrices["KES"] {
		t.Errorf("KE starter amount=%d, want %d", amt, starter.LocalPrices["KES"])
	}
	amt, cur = starter.PriceFor("US")
	if cur != "USD" || amt != 1000 {
		t.Errorf("US starter: got (%d, %s), want (1000, USD)", amt, cur)
	}
	// Africa country without a local-currency column falls back to USD.
	amt, cur = starter.PriceFor("BI") // Burundi — African but no BIF column
	if cur != "USD" || amt != 1000 {
		t.Errorf("BI starter fallback: got (%d, %s), want (1000, USD)", amt, cur)
	}
}

func TestCatalogCompleteness(t *testing.T) {
	// Every tier must have the three IDs the frontend and onboarding
	// form rely on. Guardrail against accidental renames.
	for _, id := range []PlanID{PlanStarter, PlanPro, PlanManaged} {
		p, ok := Catalog[id]
		if !ok {
			t.Errorf("catalog missing %q", id)
			continue
		}
		if p.USDCents <= 0 {
			t.Errorf("%s USDCents=%d, must be >0", id, p.USDCents)
		}
		if p.Interval != "monthly" {
			t.Errorf("%s Interval=%s, want monthly", id, p.Interval)
		}
	}
}

// ── router ─────────────────────────────────────────────────────────

// providerStub is a Provider-compliant stub for router tests.
type providerStub struct{ n string }

func (s *providerStub) Name() string { return s.n }
func (s *providerStub) CreateCheckout(_ context.Context, _ CheckoutRequest) (CheckoutResponse, error) {
	return CheckoutResponse{}, nil
}
func (s *providerStub) VerifyWebhook(_ *http.Request, _ []byte) error { return nil }
func (s *providerStub) ParseWebhook(_ []byte) (WebhookEvent, error)   { return WebhookEvent{}, nil }

func TestRouter_PicksAfrica(t *testing.T) {
	dusu := &providerStub{n: "dusupay"}
	plar := &providerStub{n: "plar"}
	r := NewRouter(dusu, plar)

	got, err := r.Pick("KE")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name() != "dusupay" {
		t.Errorf("KE -> %s, want dusupay", got.Name())
	}
	got, err = r.Pick("US")
	if err != nil || got.Name() != "plar" {
		t.Errorf("US -> %v err=%v, want plar", got, err)
	}
}

func TestRouter_NilAdapterErrors(t *testing.T) {
	plar := &providerStub{n: "plar"}
	r := NewRouter(nil, plar)
	if _, err := r.Pick("KE"); err == nil {
		t.Error("nil africa adapter should error on KE")
	}
	if _, err := r.Pick("US"); err != nil {
		t.Errorf("US should succeed, got %v", err)
	}
}

func TestRouter_NilReceiver(t *testing.T) {
	var r *Router
	if _, err := r.Pick("US"); err == nil {
		t.Error("nil router must error, not panic")
	}
}

func TestCountryFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("CF-IPCountry", "KE")
	if got := CountryFromRequest(r); got != "KE" {
		t.Errorf("CF-IPCountry=KE -> %q", got)
	}
	// Query override.
	r2 := httptest.NewRequest("GET", "/?country=ng", nil)
	if got := CountryFromRequest(r2); got != "NG" {
		t.Errorf("?country=ng -> %q, want NG", got)
	}
	// Empty → empty.
	r3 := httptest.NewRequest("GET", "/", nil)
	if got := CountryFromRequest(r3); got != "" {
		t.Errorf("no header -> %q, want empty", got)
	}
}

// ── DusuPay ────────────────────────────────────────────────────────

func TestDusuPay_VerifyWebhook(t *testing.T) {
	d := NewDusuPay(DusuPayConfig{WebhookSecret: "shh"}, nil)
	body := []byte(`{"status":"successful"}`)
	mac := hmac.New(sha256.New, []byte("shh"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-Dusupay-Signature", sig)
	if err := d.VerifyWebhook(req, body); err != nil {
		t.Errorf("valid sig rejected: %v", err)
	}

	req.Header.Set("X-Dusupay-Signature", "deadbeef")
	if err := d.VerifyWebhook(req, body); err == nil {
		t.Error("bad sig accepted")
	}

	reqNoSig := httptest.NewRequest("POST", "/", nil)
	if err := d.VerifyWebhook(reqNoSig, body); err == nil {
		t.Error("missing sig accepted")
	}
}

func TestDusuPay_ParseWebhook_Paid(t *testing.T) {
	d := NewDusuPay(DusuPayConfig{WebhookSecret: "s"}, nil)
	body := []byte(`{
		"id":"evt_1",
		"status":"successful",
		"transaction_reference":"tx_42",
		"merchant_reference":"user_abc:pro",
		"customer_reference":"user_abc",
		"metadata":{"profile_id":"user_abc","plan_id":"pro"}
	}`)
	ev, err := d.ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status != StatusPaid {
		t.Errorf("status=%s want paid", ev.Status)
	}
	if ev.ProfileID != "user_abc" || ev.PlanID != PlanPro {
		t.Errorf("got profile=%s plan=%s", ev.ProfileID, ev.PlanID)
	}
	if ev.SubscriptionID != "tx_42" {
		t.Errorf("subscription_id=%s want tx_42", ev.SubscriptionID)
	}
}

func TestDusuPay_ParseWebhook_FallsBackToMerchantRef(t *testing.T) {
	d := NewDusuPay(DusuPayConfig{}, nil)
	// No metadata — fall back to parsing merchant_reference.
	body := []byte(`{
		"id":"evt_2",
		"status":"successful",
		"transaction_reference":"tx_99",
		"merchant_reference":"user_xyz:starter"
	}`)
	ev, err := d.ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ProfileID != "user_xyz" || ev.PlanID != PlanStarter {
		t.Errorf("fallback: profile=%s plan=%s", ev.ProfileID, ev.PlanID)
	}
}

func TestDusuPay_ParseWebhook_UnknownStatus(t *testing.T) {
	d := NewDusuPay(DusuPayConfig{}, nil)
	body := []byte(`{"id":"e","status":"weird_status"}`)
	_, err := d.ParseWebhook(body)
	if err == nil || !strings.Contains(err.Error(), "not handled") {
		t.Errorf("unknown status -> %v, want ErrUnhandledEvent wrap", err)
	}
}

// ── Plar ───────────────────────────────────────────────────────────

func TestPlar_VerifyWebhook(t *testing.T) {
	p := NewPlar(PlarConfig{WebhookSecret: "shh"}, nil)
	body := []byte(`{"type":"checkout.completed"}`)
	mac := hmac.New(sha256.New, []byte("shh"))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-Plar-Signature", sig)
	if err := p.VerifyWebhook(req, body); err != nil {
		t.Errorf("valid sig rejected: %v", err)
	}

	req.Header.Set("X-Plar-Signature", "deadbeef")
	if err := p.VerifyWebhook(req, body); err == nil {
		t.Error("bad sig accepted")
	}
}

func TestPlar_ParseWebhook_CheckoutCompleted(t *testing.T) {
	p := NewPlar(PlarConfig{}, nil)
	body := []byte(`{
		"id":"evt_p1",
		"type":"checkout.completed",
		"data":{
			"checkout_id":"ck_100",
			"subscription_id":"sub_42",
			"external_id":"user_abc",
			"metadata":{"profile_id":"user_abc","plan_id":"pro"}
		}
	}`)
	ev, err := p.ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status != StatusPaid {
		t.Errorf("status=%s want paid", ev.Status)
	}
	if ev.SubscriptionID != "sub_42" {
		t.Errorf("sub_id=%s want sub_42", ev.SubscriptionID)
	}
	if ev.ProfileID != "user_abc" || ev.PlanID != PlanPro {
		t.Errorf("got profile=%s plan=%s", ev.ProfileID, ev.PlanID)
	}
}

func TestPlar_ParseWebhook_SubscriptionCancelled(t *testing.T) {
	p := NewPlar(PlarConfig{}, nil)
	body := []byte(`{"id":"e","type":"subscription.cancelled","data":{"subscription_id":"s","metadata":{"profile_id":"u","plan_id":"pro"}}}`)
	ev, err := p.ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status != StatusCancelled {
		t.Errorf("status=%s want cancelled", ev.Status)
	}
}

func TestPlar_ParseWebhook_UnknownType(t *testing.T) {
	p := NewPlar(PlarConfig{}, nil)
	body := []byte(`{"type":"charge.disputed","status":""}`)
	if _, err := p.ParseWebhook(body); err == nil {
		t.Error("unknown type should error with ErrUnhandledEvent")
	}
}
