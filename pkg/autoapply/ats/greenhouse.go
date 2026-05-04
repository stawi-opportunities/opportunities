// Package ats contains ATS-specific application submitters.
package ats

import (
	"context"
	"errors"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// GreenhouseSubmitter handles Greenhouse job board application forms.
// Greenhouse uses stable HTML form IDs across all hosted boards
// (boards.greenhouse.io/<company>/jobs/<id>), making it the most
// reliable ATS-UI target.
type GreenhouseSubmitter struct {
	client browser.ApplyClient
}

// NewGreenhouseSubmitter wires the submitter with an ApplyClient.
func NewGreenhouseSubmitter(client browser.ApplyClient) *GreenhouseSubmitter {
	return &GreenhouseSubmitter{client: client}
}

func (s *GreenhouseSubmitter) Name() string { return "greenhouse_ui" }

func (s *GreenhouseSubmitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
	if sourceType == domain.SourceGreenhouse {
		return true
	}
	lower := strings.ToLower(applyURL)
	return strings.Contains(lower, "boards.greenhouse.io") ||
		strings.Contains(lower, "gh_jid=") ||
		strings.Contains(lower, "greenhouse.io/jobs")
}

func (s *GreenhouseSubmitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	firstName, lastName := splitName(req.FullName)

	textFields := map[string]string{
		"#first_name":                            firstName,
		"#last_name":                             lastName,
		"#email":                                 req.Email,
		"#phone":                                 req.Phone,
		"#job_application_first_name":            firstName,
		"#job_application_last_name":             lastName,
		"#job_application_email":                 req.Email,
		"#job_application_phone":                 req.Phone,
	}
	if req.CoverLetter != "" {
		textFields["#cover_letter_text"] = req.CoverLetter
		textFields["#job_application_cover_letter_text"] = req.CoverLetter
	}

	err := s.client.FillAndSubmit(ctx, browser.SubmitOptions{
		URL:         req.ApplyURL,
		TextFields:  textFields,
		FileField:   "#resume, #job_application_resume",
		FileBytes:   req.CVBytes,
		FileName:    req.CVFilename,
		SubmitSel:   "[data-submit='true'], input[type='submit'], button[type='submit']",
		ConfirmText: "thank you for applying",
	})
	if err != nil {
		if errors.Is(err, browser.ErrCAPTCHA) {
			return autoapply.SubmitResult{Method: "skipped", SkipReason: "captcha"}, nil
		}
		if errors.Is(err, browser.ErrElementNotFound) {
			return autoapply.SubmitResult{Method: "skipped", SkipReason: "unsupported"}, nil
		}
		if errors.Is(err, browser.ErrSubmitNotConfirmed) {
			return autoapply.SubmitResult{Method: "skipped", SkipReason: "not_confirmed"}, nil
		}
		return autoapply.SubmitResult{}, err
	}

	return autoapply.SubmitResult{Method: "ats_ui"}, nil
}
