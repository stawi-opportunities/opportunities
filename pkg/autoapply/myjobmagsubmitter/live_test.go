package myjobmagsubmitter

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/captcha"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// TestLive_OnSiteForm drives the real on-site /job-application flow against
// a live MyJobMag form: GET → scrape → solve the reCAPTCHA via 2captcha →
// POST the application. It is skipped unless MYJOBMAG_LIVE=1.
//
// WARNING: this submits a REAL application to the employer and spends a
// real 2captcha credit. Point it at a /job-application/<id> URL you are
// genuinely willing to apply to with your own details.
//
// Run (from the repo root, key already in .env):
//
//	MYJOBMAG_LIVE=1 \
//	MYJOBMAG_APPLY_URL='https://www.myjobmag.co.ke/job-application/1263967' \
//	MYJOBMAG_CV_PATH="$HOME/cv.pdf" \
//	go test ./pkg/autoapply/myjobmagsubmitter/ -run TestLive_OnSiteForm -v -count=1 -timeout 5m
//
// Candidate fields default to sensible test values; override any via env:
//
//	MYJOBMAG_NAME, MYJOBMAG_EMAIL, MYJOBMAG_PHONE, MYJOBMAG_LOCATION, MYJOBMAG_COVER
func TestLive_OnSiteForm(t *testing.T) {
	if os.Getenv("MYJOBMAG_LIVE") != "1" {
		t.Skip("set MYJOBMAG_LIVE=1 to run the live on-site form submission")
	}
	loadDotEnv(t)

	applyURL := os.Getenv("MYJOBMAG_APPLY_URL")
	if applyURL == "" {
		t.Fatal("MYJOBMAG_APPLY_URL is required (a /job-application/<id> URL)")
	}
	if !jobAppPathRE.MatchString(applyURL) {
		t.Fatalf("MYJOBMAG_APPLY_URL should be a /job-application/<id> URL to force the on-site form path, got %q", applyURL)
	}

	apiKey := firstEnv("AUTO_APPLY_CAPTCHA_API_KEY", "TWO_CAPTCHA_API_KEY", "TWOCAPTCHA_API_KEY")
	if apiKey == "" {
		t.Fatal("no 2captcha key found (AUTO_APPLY_CAPTCHA_API_KEY / TWO_CAPTCHA_API_KEY)")
	}

	cvPath := os.Getenv("MYJOBMAG_CV_PATH")
	if cvPath == "" {
		t.Fatal("MYJOBMAG_CV_PATH is required (path to a CV PDF)")
	}
	cvBytes, err := os.ReadFile(cvPath)
	if err != nil {
		t.Fatalf("read CV %q: %v", cvPath, err)
	}

	solver := captcha.NewTwoCaptcha(apiKey).WithTimeout(180 * time.Second)
	s := New(Config{Captcha: solver, HTTPTimeout: 60 * time.Second})

	req := autoapply.SubmitRequest{
		SourceType:  domain.SourceMyJobMag,
		ApplyURL:    applyURL,
		FullName:    envOr("MYJOBMAG_NAME", "Joakim Bwire"),
		Email:       envOr("MYJOBMAG_EMAIL", "joakimbwire23@gmail.com"),
		Phone:       envOr("MYJOBMAG_PHONE", "0741697305"),
		Location:    envOr("MYJOBMAG_LOCATION", "Nairobi"),
		CoverLetter: envOr("MYJOBMAG_COVER", "Dear Hiring Manager,\n\nI am writing to express my interest in this role and have attached my CV for your consideration.\n\nBest regards,\nJoakim Bwire"),
		CVBytes:     cvBytes,
		CVFilename:  filepath.Base(cvPath),
	}

	// Capture the full POST response so we can inspect MyJobMag's real
	// success/error page and tighten success detection.
	dumpPath := envOr("MYJOBMAG_DUMP", "/tmp/myjobmag_response.html")
	debugResponseSink = func(b []byte) {
		if werr := os.WriteFile(dumpPath, b, 0o600); werr != nil {
			t.Logf("dump response failed: %v", werr)
		} else {
			t.Logf("response body written to %s (%d bytes)", dumpPath, len(b))
		}
	}
	defer func() { debugResponseSink = nil }()

	t.Logf("submitting live application to %s (solving reCAPTCHA, may take ~30-120s)...", applyURL)
	res, err := s.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned a transient error: %v", err)
	}
	t.Logf("RESULT: method=%q skip_reason=%q external_ref=%q", res.Method, res.SkipReason, res.ExternalRef)
	if res.Method != "myjobmag_form" {
		t.Fatalf("expected method myjobmag_form, got %q (skip_reason=%q)", res.Method, res.SkipReason)
	}
	t.Log("SUCCESS: application POSTed and accepted (no error markers in response)")
}

// loadDotEnv loads KEY=VALUE pairs from the repo-root .env into the process
// environment (without overwriting values already set), so the 2captcha key
// placed in .env is picked up automatically.
func loadDotEnv(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for i := 0; i < 6; i++ {
		path := filepath.Join(dir, ".env")
		if f, ferr := os.Open(path); ferr == nil {
			defer func() { _ = f.Close() }()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				key, val, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				key = strings.TrimSpace(key)
				val = strings.Trim(strings.TrimSpace(val), `"'`)
				if _, exists := os.LookupEnv(key); !exists {
					_ = os.Setenv(key, val)
				}
			}
			t.Logf("loaded .env from %s", path)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
