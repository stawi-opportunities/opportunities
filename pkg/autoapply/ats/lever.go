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
	client *browser.ApplyClient
}

func NewLeverSubmitter(client *browser.ApplyClient) *LeverSubmitter {
	return &LeverSubmitter{client: client}
}

func (s *LeverSubmitter) Name() string { return "lever_ui" }

func (s *LeverSubmitter) CanHandle(sourceType domain.SourceType, applyURL string) bool {
	if sourceType == domain.SourceLever {
		return true
	}
	return strings.Contains(strings.ToLower(applyURL), "jobs.lever.co")
}

func (s *LeverSubmitter) Submit(ctx context.Context, req autoapply.SubmitRequest) (autoapply.SubmitResult, error) {
	textFields := map[string]string{
		"[name='name']":  req.FullName,
		"[name='email']": req.Email,
		"[name='phone']": req.Phone,
		"[name='org']":   req.CurrentTitle,
	}
	if req.CoverLetter != "" {
		textFields["[name='comments']"] = req.CoverLetter
	}

	err := s.client.FillAndSubmit(
		ctx,
		req.ApplyURL,
		textFields,
		"[name='resume']",
		req.CVBytes,
		req.CVFilename,
		"[data-qa='btn-submit-application'], button[type='submit']",
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
