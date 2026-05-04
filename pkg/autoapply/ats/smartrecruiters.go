package ats

import (
	"context"
	"errors"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// SmartRecruitersSubmitter handles SmartRecruiters application forms
// (jobs.smartrecruiters.com/<company>/<job-id>).
type SmartRecruitersSubmitter struct {
	client browser.ApplyClient
}

func NewSmartRecruitersSubmitter(client browser.ApplyClient) *SmartRecruitersSubmitter {
	return &SmartRecruitersSubmitter{client: client}
}

func (s *SmartRecruitersSubmitter) Name() string { return "smartrecruiters_ui" }

func (s *SmartRecruitersSubmitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
	if sourceType == domain.SourceSmartRecruitersPage || sourceType == domain.SourceSmartRecruitersAPI {
		return true
	}
	return strings.Contains(strings.ToLower(applyURL), "smartrecruiters.com")
}

func (s *SmartRecruitersSubmitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	firstName, lastName := splitName(req.FullName)

	textFields := map[string]string{
		"[name='firstName']":   firstName,
		"[name='lastName']":    lastName,
		"[name='email']":       req.Email,
		"[name='phoneNumber']": req.Phone,
	}

	err := s.client.FillAndSubmit(ctx, browser.SubmitOptions{
		URL:         req.ApplyURL,
		TextFields:  textFields,
		FileField:   "input[type='file']",
		FileBytes:   req.CVBytes,
		FileName:    req.CVFilename,
		SubmitSel:   "[data-test-id='apply-button-bottom'], button[type='submit']",
		ConfirmText: "application sent",
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

// splitName splits "First Last" into (First, Last). When the name has
// no space, the full string is used as first name and last name is "".
// Multi-word last names ("Anne Marie Smith") fold the tail into Last.
// Shared by all ATS submitters via this unexported helper.
func splitName(full string) (string, string) {
	full = strings.TrimSpace(full)
	idx := strings.Index(full, " ")
	if idx < 0 {
		return full, ""
	}
	return full[:idx], strings.TrimSpace(full[idx+1:])
}
