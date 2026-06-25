package ats

import (
	"context"
	"errors"
	"strings"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// LeverSubmitter handles Lever job board application forms
// (jobs.lever.co/<company>/<job-id>).
type LeverSubmitter struct {
	client browser.ApplyClient
}

func NewLeverSubmitter(client browser.ApplyClient) *LeverSubmitter {
	return &LeverSubmitter{client: client}
}

func (s *LeverSubmitter) Name() string { return "lever_ui" }

func (s *LeverSubmitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
	if sourceType == domain.SourceLever {
		return true
	}
	return strings.Contains(strings.ToLower(applyURL), "jobs.lever.co") ||
		strings.Contains(strings.ToLower(applyURL), "lever.co/")
}

func (s *LeverSubmitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	// Lever's [name='org'] is the candidate's *current company*, not a
	// job title. We don't carry that field on SubmitRequest yet, so we
	// leave it unset rather than seeding it with garbage.
	textFields := map[string]string{
		"[name='name']":  req.FullName,
		"[name='email']": req.Email,
		"[name='phone']": req.Phone,
	}
	if req.CoverLetter != "" {
		textFields["[name='comments']"] = req.CoverLetter
	}

	err := s.client.FillAndSubmit(ctx, browser.SubmitOptions{
		URL:         req.ApplyURL,
		TextFields:  textFields,
		FileField:   "[name='resume']",
		FileBytes:   req.CVBytes,
		FileName:    req.CVFilename,
		SubmitSel:   "[data-qa='btn-submit-application'], button[type='submit']",
		ConfirmText: "application submitted",
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
