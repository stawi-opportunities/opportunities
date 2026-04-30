package ats

import (
	"context"
	"errors"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// WorkdaySubmitter handles Workday application forms. Workday uses
// data-automation-id attributes consistently across all tenant instances,
// which makes it feasible to automate despite the AJAX-heavy UI.
type WorkdaySubmitter struct {
	client *browser.ApplyClient
}

func NewWorkdaySubmitter(client *browser.ApplyClient) *WorkdaySubmitter {
	return &WorkdaySubmitter{client: client}
}

func (s *WorkdaySubmitter) Name() string { return "workday_ui" }

func (s *WorkdaySubmitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
	if sourceType == domain.SourceWorkday {
		return true
	}
	lower := strings.ToLower(applyURL)
	return strings.Contains(lower, "myworkdayjobs.com") ||
		strings.Contains(lower, "wd3.myworkday.com") ||
		strings.Contains(lower, "wd5.myworkday.com")
}

func (s *WorkdaySubmitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	firstName, lastName := splitName(req.FullName)

	textFields := map[string]string{
		"[data-automation-id='firstName']":   firstName,
		"[data-automation-id='lastName']":    lastName,
		"[data-automation-id='email']":       req.Email,
		"[data-automation-id='phone-number']": req.Phone,
	}

	err := s.client.FillAndSubmit(
		ctx,
		req.ApplyURL,
		textFields,
		"[data-automation-id='file-upload-input-ref']",
		req.CVBytes,
		req.CVFilename,
		"[data-automation-id='bottom-navigation-next-button'], button[data-automation-id='saveAndContinueButton']",
	)
	if err != nil {
		if errors.Is(err, browser.ErrCAPTCHA) {
			return autoapply.SubmitResult{Method: "skipped", SkipReason: "captcha"}, nil
		}
		if errors.Is(err, browser.ErrElementNotFound) {
			return autoapply.SubmitResult{Method: "skipped", SkipReason: "unsupported"}, nil
		}
		return autoapply.SubmitResult{}, err
	}

	return autoapply.SubmitResult{Method: "ats_ui"}, nil
}
