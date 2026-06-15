package ats

import (
	"context"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/otprendezvous"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// fakeApplyClient records the options it was handed and, when the
// submitter wired an OTP CodeProvider, drives it the way the real
// browser would (call it, surface ErrOTPTimeout on error).
type fakeApplyClient struct {
	lastOpts browser.SubmitOptions
	gotCode  string
	html     string                    // returned by GetHTML
	fields   []browser.FieldDescriptor // returned by GetFormFields

	// deliver, when set, models the OTP-email ingress: the code is Put
	// into the rendezvous at the moment the first submit completes and
	// the security-code field appears, i.e. just before CodeProvider runs.
	deliver func()
}

func (f *fakeApplyClient) FillAndSubmit(ctx context.Context, opts browser.SubmitOptions) error {
	f.lastOpts = opts
	if opts.CodeProvider != nil {
		if f.deliver != nil {
			f.deliver()
		}
		code, err := opts.CodeProvider(ctx)
		if err != nil {
			return browser.ErrOTPTimeout
		}
		f.gotCode = code
	}
	return nil
}

func (f *fakeApplyClient) GetHTML(context.Context, string) (string, error) { return f.html, nil }
func (f *fakeApplyClient) GetFormFields(context.Context, string) ([]browser.FieldDescriptor, error) {
	return f.fields, nil
}
func (f *fakeApplyClient) Close() {}

func ghRequest() autoapply.SubmitRequest {
	return autoapply.SubmitRequest{
		SourceType: domain.SourceGreenhouse,
		ApplyURL:   "https://boards.greenhouse.io/cloudflare/jobs/123",
		Email:      "joakim@example.com",
		FullName:   "Joakim B",
	}
}

func TestGreenhouseOTPSuccess(t *testing.T) {
	rdv := otprendezvous.NewInMemory()
	ctx := context.Background()

	// The submitter Clears the key before submitting, then the ingress
	// delivers the emailed code mid-hold. Model that with deliver.
	key := otprendezvous.Key("joakim@example.com", "cloudflare")
	fc := &fakeApplyClient{
		deliver: func() { _ = rdv.Put(ctx, key, "51iL5qrq") },
	}
	s := NewGreenhouseSubmitterWithOTP(fc, rdv, time.Second)

	res, err := s.Submit(ctx, ghRequest())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Method != "ats_ui" {
		t.Fatalf("Method = %q, want ats_ui", res.Method)
	}
	if fc.lastOpts.OTPFieldSel == "" || fc.lastOpts.CodeProvider == nil {
		t.Fatal("expected OTP field + CodeProvider to be wired")
	}
	if fc.gotCode != "51iL5qrq" {
		t.Fatalf("code passed to browser = %q, want 51iL5qrq", fc.gotCode)
	}
}

func TestGreenhouseOTPTimeout(t *testing.T) {
	rdv := otprendezvous.NewInMemory() // nothing ever Put
	fc := &fakeApplyClient{}
	s := NewGreenhouseSubmitterWithOTP(fc, rdv, 50*time.Millisecond)

	res, err := s.Submit(context.Background(), ghRequest())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Method != "skipped" || res.SkipReason != "otp_timeout" {
		t.Fatalf("got %+v, want skipped/otp_timeout", res)
	}
}

func TestGreenhouseNoOTPWhenDisabled(t *testing.T) {
	fc := &fakeApplyClient{}
	s := NewGreenhouseSubmitter(fc) // no rendezvous

	if _, err := s.Submit(context.Background(), ghRequest()); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if fc.lastOpts.CodeProvider != nil || fc.lastOpts.OTPFieldSel != "" {
		t.Fatal("OTP must not be wired when no rendezvous is configured")
	}
}
