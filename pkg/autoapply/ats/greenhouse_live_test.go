//go:build manual

package ats

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/apps/autoapply/service"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/otprendezvous"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// TestLiveGreenhouseSubmitFillOnly runs the REAL GreenhouseSubmitter against
// a live board in fill-only mode: standard fields + CV + AI-answered custom
// questions are filled and a screenshot saved, but Submit is never clicked,
// so no application is sent.
//
//	GREENHOUSE_TEST_URL=https://job-boards.greenhouse.io/<co>/jobs/<id> \
//	INFERENCE_BASE_URL=... INFERENCE_API_KEY=... INFERENCE_MODEL=... \
//	GREENHOUSE_CV=/path/resume.pdf  GREENHOUSE_SHOT=/tmp/gh_real.png \
//	  go test -tags=manual -run TestLiveGreenhouseSubmitFillOnly -v -timeout 240s ./pkg/autoapply/ats/
func TestLiveGreenhouseSubmitFillOnly(t *testing.T) {
	url := os.Getenv("GREENHOUSE_TEST_URL")
	if url == "" {
		t.Skip("set GREENHOUSE_TEST_URL to run")
	}
	shot := os.Getenv("GREENHOUSE_SHOT")
	if shot == "" {
		shot = "/tmp/gh_real.png"
	}

	pool := browser.NewPool(1, 90*time.Second, "")
	defer pool.Close()

	s := NewGreenhouseSubmitter(pool).WithFillOnly(shot)
	if base := os.Getenv("INFERENCE_BASE_URL"); base != "" {
		s = s.WithLLM(&liveLLM{
			base:  base,
			key:   os.Getenv("INFERENCE_API_KEY"),
			model: os.Getenv("INFERENCE_MODEL"),
			http:  &http.Client{Timeout: 60 * time.Second},
		})
		t.Logf("LLM custom-question answering ENABLED (%s)", os.Getenv("INFERENCE_MODEL"))
	} else {
		t.Log("LLM disabled (set INFERENCE_BASE_URL to test custom questions)")
	}

	var cv []byte
	if p := os.Getenv("GREENHOUSE_CV"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read CV %s: %v", p, err)
		}
		cv = b
	}

	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType:   domain.SourceGreenhouse,
		ApplyURL:     url,
		CandidateID:  "cnd_live_test",
		FullName:     "Test User",
		Email:        "test.user@example.com",
		Phone:        "+254712345678",
		Location:     "Nairobi, Kenya",
		CurrentTitle: "Account Executive",
		Skills:       "B2B sales, SaaS, pipeline management, negotiation",
		CoverLetter:  "I am excited to apply and bring strong B2B SaaS sales experience.",
		CVBytes:      cv,
		CVFilename:   "resume.pdf",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	fmt.Printf("RESULT: method=%q skip=%q ref=%q  (screenshot: %s)\n",
		res.Method, res.SkipReason, res.ExternalRef, shot)
}

// TestLiveGreenhouseLLMAnswers isolates the AI step: it fetches the live
// form HTML and prints exactly what AnswerFormFields returns, so we can
// see whether the LLM is producing usable selector→value answers.
//
//	GREENHOUSE_TEST_URL=... INFERENCE_BASE_URL=... INFERENCE_API_KEY=... INFERENCE_MODEL=... \
//	  go test -tags=manual -run TestLiveGreenhouseLLMAnswers -v -timeout 180s ./pkg/autoapply/ats/
func TestLiveGreenhouseLLMAnswers(t *testing.T) {
	url := os.Getenv("GREENHOUSE_TEST_URL")
	base := os.Getenv("INFERENCE_BASE_URL")
	if url == "" || base == "" {
		t.Skip("set GREENHOUSE_TEST_URL and INFERENCE_BASE_URL")
	}

	pool := browser.NewPool(1, 90*time.Second, "")
	defer pool.Close()

	fields, err := pool.GetFormFields(context.Background(), url)
	if err != nil {
		t.Fatalf("GetFormFields: %v", err)
	}
	fmt.Printf("EXTRACTED FIELDS (%d):\n", len(fields))
	for _, f := range fields {
		fmt.Printf("  %s [%s req=%v] %q opts=%v\n", f.Selector, f.Type, f.Required, f.Label, f.Options)
	}

	llm := &liveLLM{base: base, key: os.Getenv("INFERENCE_API_KEY"), model: os.Getenv("INFERENCE_MODEL"), http: &http.Client{Timeout: 60 * time.Second}}
	answers, err := autoapply.AnswerFieldsStructured(context.Background(), llm, fields, autoapply.FieldAnswerProfile{
		FullName:     "Test User",
		Email:        "test.user@example.com",
		Phone:        "+254712345678",
		Location:     "Nairobi, Kenya",
		CurrentTitle: "Account Executive",
		Skills:       "B2B sales, SaaS, pipeline management",
		CoverLetter:  "Excited to bring B2B SaaS sales experience.",
	})
	if err != nil {
		t.Fatalf("AnswerFieldsStructured: %v", err)
	}
	fmt.Printf("LLM ANSWERS (%d):\n", len(answers))
	for sel, val := range answers {
		fmt.Printf("  %s = %q\n", sel, val)
	}
}

// TestLiveGreenhouseSubmitObserve does a REAL submit (fills standard + CV +
// AI custom answers, then clicks Submit) WITHOUT a CAPTCHA solver and
// WITHOUT OTP wiring, capturing the post-submit page so we can see where
// the security-code field (or a CAPTCHA) loads.
//
// WARNING: this sends a real application. Point it at a throwaway/sandbox.
//
//	GREENHOUSE_TEST_URL=... INFERENCE_BASE_URL=... INFERENCE_API_KEY=... INFERENCE_MODEL=... \
//	GREENHOUSE_CV=/tmp/resume.pdf GREENHOUSE_SHOT=/tmp/gh_postsubmit.png \
//	  go test -tags=manual -run TestLiveGreenhouseSubmitObserve -v -timeout 240s ./pkg/autoapply/ats/
func TestLiveGreenhouseSubmitObserve(t *testing.T) {
	url := os.Getenv("GREENHOUSE_TEST_URL")
	if url == "" {
		t.Skip("set GREENHOUSE_TEST_URL to run")
	}
	shot := os.Getenv("GREENHOUSE_SHOT")
	if shot == "" {
		shot = "/tmp/gh_postsubmit.png"
	}

	pool := browser.NewPool(1, 90*time.Second, "")
	defer pool.Close()

	s := NewGreenhouseSubmitter(pool).WithPostSubmitShot(shot)
	if base := os.Getenv("INFERENCE_BASE_URL"); base != "" {
		s = s.WithLLM(&liveLLM{base: base, key: os.Getenv("INFERENCE_API_KEY"), model: os.Getenv("INFERENCE_MODEL"), http: &http.Client{Timeout: 60 * time.Second}})
	}

	var cv []byte
	if p := os.Getenv("GREENHOUSE_CV"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read CV: %v", err)
		}
		cv = b
	}

	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType:   domain.SourceGreenhouse,
		ApplyURL:     url,
		CandidateID:  "cnd_live_test",
		FullName:     "Test User",
		Email:        "test.user@example.com",
		Phone:        "+254712345678",
		Location:     "Nairobi, Kenya",
		CurrentTitle: "Account Executive",
		Skills:       "B2B sales, SaaS, pipeline management, negotiation",
		CoverLetter:  "I am excited to apply and bring strong B2B SaaS sales experience.",
		CVBytes:      cv,
		CVFilename:   "resume.pdf",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	fmt.Printf("RESULT: method=%q skip=%q ref=%q  (post-submit screenshot: %s)\n",
		res.Method, res.SkipReason, res.ExternalRef, shot)
}

// TestLiveGreenhouseOTPEntry drives a REAL submit to the security-code gate,
// then injects a DUMMY 8-char code via the rendezvous to verify our
// segmented-input entry fills all boxes. The dummy code is wrong, so the
// application is NOT completed — we only confirm the entry mechanism via the
// "<shot>.entered.png" screenshot.
//
//	GREENHOUSE_TEST_URL=... INFERENCE_BASE_URL=... INFERENCE_API_KEY=... INFERENCE_MODEL=... \
//	GREENHOUSE_CV=/tmp/resume.pdf GREENHOUSE_SHOT=/tmp/gh_otp.png \
//	  go test -tags=manual -run TestLiveGreenhouseOTPEntry -v -timeout 300s ./pkg/autoapply/ats/
func TestLiveGreenhouseOTPEntry(t *testing.T) {
	url := os.Getenv("GREENHOUSE_TEST_URL")
	if url == "" {
		t.Skip("set GREENHOUSE_TEST_URL to run")
	}
	shot := os.Getenv("GREENHOUSE_SHOT")
	if shot == "" {
		shot = "/tmp/gh_otp.png"
	}

	pool := browser.NewPool(1, 90*time.Second, "")
	defer pool.Close()

	rdv := otprendezvous.NewInMemory()
	email := "test.user@example.com"
	company := otprendezvous.CompanyFromGreenhouseURL(url)
	key := otprendezvous.Key(email, company)

	// Keep the dummy code available across the submitter's Clear+Poll window.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = rdv.Put(context.Background(), key, "ABCD1234")
				time.Sleep(2 * time.Second)
			}
		}
	}()

	s := NewGreenhouseSubmitterWithOTP(pool, rdv, 90*time.Second).WithPostSubmitShot(shot)
	if base := os.Getenv("INFERENCE_BASE_URL"); base != "" {
		s = s.WithLLM(&liveLLM{base: base, key: os.Getenv("INFERENCE_API_KEY"), model: os.Getenv("INFERENCE_MODEL"), http: &http.Client{Timeout: 60 * time.Second}})
	}

	var cv []byte
	if p := os.Getenv("GREENHOUSE_CV"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read CV: %v", err)
		}
		cv = b
	}

	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType:   domain.SourceGreenhouse,
		ApplyURL:     url,
		CandidateID:  "cnd_live_test",
		FullName:     "Test User",
		Email:        email,
		Phone:        "+254712345678",
		Location:     "Nairobi, Kenya",
		CurrentTitle: "Account Executive",
		Skills:       "B2B sales, SaaS, pipeline management, negotiation",
		CoverLetter:  "I am excited to apply and bring strong B2B SaaS sales experience.",
		CVBytes:      cv,
		CVFilename:   "resume.pdf",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	fmt.Printf("RESULT: method=%q skip=%q  (code-entered screenshot: %s.entered.png)\n",
		res.Method, res.SkipReason, shot)
}

// TestLiveGreenhouseOTPWebhook runs the PRODUCTION OTP-ingress path: the real
// /webhooks/otp handler is mounted on a local HTTP server sharing the same
// rendezvous the held submitter polls. Submit holds at the security-code gate;
// POST the raw Greenhouse OTP email to the webhook (simulating the inbound
// provider) and the submitter enters the code and completes.
//
// WARNING: completes a real application.
//
//	GREENHOUSE_TEST_URL=... GREENHOUSE_EMAIL=you@gmail.com \
//	INFERENCE_BASE_URL=... INFERENCE_API_KEY=... INFERENCE_MODEL=... \
//	GREENHOUSE_CV=/tmp/resume.pdf GREENHOUSE_SHOT=/tmp/gh_wh.png OTP_WH_SECRET=whsecret OTP_WH_ADDR=:8099 \
//	  go test -tags=manual -run TestLiveGreenhouseOTPWebhook -v -timeout 420s ./pkg/autoapply/ats/
func TestLiveGreenhouseOTPWebhook(t *testing.T) {
	url := os.Getenv("GREENHOUSE_TEST_URL")
	email := os.Getenv("GREENHOUSE_EMAIL")
	if url == "" || email == "" {
		t.Skip("set GREENHOUSE_TEST_URL and GREENHOUSE_EMAIL to run")
	}
	shot := os.Getenv("GREENHOUSE_SHOT")
	if shot == "" {
		shot = "/tmp/gh_wh.png"
	}
	secret := os.Getenv("OTP_WH_SECRET")
	if secret == "" {
		secret = "whsecret"
	}
	addr := os.Getenv("OTP_WH_ADDR")
	if addr == "" {
		addr = ":8099"
	}

	pool := browser.NewPool(1, 90*time.Second, "")
	defer pool.Close()

	rdv := otprendezvous.NewInMemory()

	// Mount the REAL production webhook handler on the shared rendezvous.
	mux := http.NewServeMux()
	mux.Handle("POST /webhooks/otp", service.NewOTPWebhookHandler(rdv, secret, "greenhouse-mail.io"))
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() { _ = srv.ListenAndServe() }()
	defer func() { _ = srv.Close() }()
	t.Logf("webhook ready: POST raw OTP email to http://localhost%s/webhooks/otp (X-OTP-Secret: %s)", addr, secret)

	s := NewGreenhouseSubmitterWithOTP(pool, rdv, 300*time.Second).WithPostSubmitShot(shot)
	if base := os.Getenv("INFERENCE_BASE_URL"); base != "" {
		s = s.WithLLM(&liveLLM{base: base, key: os.Getenv("INFERENCE_API_KEY"), model: os.Getenv("INFERENCE_MODEL"), http: &http.Client{Timeout: 60 * time.Second}})
	}

	var cv []byte
	if p := os.Getenv("GREENHOUSE_CV"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read CV: %v", err)
		}
		cv = b
	}

	t.Logf("submitting as %s; holding at OTP gate for the webhook POST", email)
	res, err := s.Submit(context.Background(), autoapply.SubmitRequest{
		SourceType:   domain.SourceGreenhouse,
		ApplyURL:     url,
		CandidateID:  "cnd_live_test",
		FullName:     "Test User",
		Email:        email,
		Phone:        "+254712345678",
		Location:     "Nairobi, Kenya",
		CurrentTitle: "Account Executive",
		Skills:       "B2B sales, SaaS, pipeline management, negotiation",
		CoverLetter:  "I am excited to apply and bring strong B2B SaaS sales experience.",
		CVBytes:      cv,
		CVFilename:   "resume.pdf",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	fmt.Printf("RESULT: method=%q skip=%q  (final screenshot: %s.final.png)\n", res.Method, res.SkipReason, shot)
}

// liveLLM is a minimal OpenAI-compatible chat client for the manual test.
type liveLLM struct {
	base, key, model string
	http             *http.Client
}

func (c *liveLLM) Complete(ctx context.Context, system, user string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("llm http %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: empty choices")
	}
	return out.Choices[0].Message.Content, nil
}
