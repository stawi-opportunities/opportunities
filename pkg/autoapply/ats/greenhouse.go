// Package ats contains ATS-specific application submitters.
package ats

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/otprendezvous"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

// greenhouseOTPField is the security-code input Greenhouse renders after
// the first submit when it has emailed a verification code ("Copy and
// paste this code into the security code field on your application").
// Confirm/adjust against a live board.
// greenhouseOTPField matches the first box of Greenhouse's segmented
// "Security code" input (#security-input-0 … #security-input-7) that
// appears after submit when a verification code is emailed; the legacy
// single-field selectors are kept as fallbacks for other boards.
const greenhouseOTPField = "#security-input-0, #security_code, input[autocomplete='one-time-code']"

// defaultOTPWait bounds how long a browser slot is held waiting for the
// emailed code when a caller doesn't specify a wait.
const defaultOTPWait = 60 * time.Second

// GreenhouseSubmitter handles Greenhouse job board application forms.
// Greenhouse uses stable HTML form IDs across all hosted boards
// (boards.greenhouse.io/<company>/jobs/<id>), making it the most
// reliable ATS-UI target.
//
// When rdv is non-nil the submitter also satisfies Greenhouse's emailed
// "security code" gate: it holds the page open after the first submit,
// polls the rendezvous for the code (delivered by the OTP-email ingress),
// enters it, and resubmits.
type GreenhouseSubmitter struct {
	client  browser.ApplyClient
	rdv     otprendezvous.Rendezvous // nil → OTP phase disabled
	otpWait time.Duration
	llm     autoapply.LLMClient // nil → custom-question answering disabled

	// fillOnly + screenshotPath drive safe live testing: when fillOnly is
	// set the submitter fills everything (incl. CV + AI answers) but never
	// clicks Submit, so no real application is sent.
	fillOnly       bool
	screenshotPath string
	// postSubmitShot captures the page right after the submit click (to
	// observe where the OTP/security-code field or CAPTCHA loads).
	postSubmitShot string
}

// NewGreenhouseSubmitter wires the submitter with an ApplyClient and no
// OTP support (the historical behaviour).
func NewGreenhouseSubmitter(client browser.ApplyClient) *GreenhouseSubmitter {
	return &GreenhouseSubmitter{client: client}
}

// NewGreenhouseSubmitterWithOTP wires the submitter with an email-OTP
// rendezvous. wait bounds the hold-open window for the emailed code;
// non-positive values fall back to defaultOTPWait.
func NewGreenhouseSubmitterWithOTP(client browser.ApplyClient, rdv otprendezvous.Rendezvous, wait time.Duration) *GreenhouseSubmitter {
	if wait <= 0 {
		wait = defaultOTPWait
	}
	return &GreenhouseSubmitter{client: client, rdv: rdv, otpWait: wait}
}

// WithLLM enables AI-assisted answering of the form's custom questions
// (work authorization, years of experience, screening questions, …) that
// the static field map can't know in advance. Returns the receiver for
// chaining. A nil llm leaves the feature off.
func (s *GreenhouseSubmitter) WithLLM(llm autoapply.LLMClient) *GreenhouseSubmitter {
	s.llm = llm
	return s
}

// WithFillOnly puts the submitter in fill-only mode: it fills the form
// (standard fields, CV, and AI-answered custom questions) and saves a
// screenshot to screenshotPath, but never clicks Submit. Used to validate
// against a live board without sending a real application. Returns the
// receiver for chaining.
func (s *GreenhouseSubmitter) WithFillOnly(screenshotPath string) *GreenhouseSubmitter {
	s.fillOnly = true
	s.screenshotPath = screenshotPath
	return s
}

// WithPostSubmitShot captures the page immediately after the submit click
// to the given path, so we can observe where the security-code field or
// CAPTCHA loads. Returns the receiver for chaining.
func (s *GreenhouseSubmitter) WithPostSubmitShot(path string) *GreenhouseSubmitter {
	s.postSubmitShot = path
	return s
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

	// AI-assisted custom questions: the static map above covers the
	// standard Greenhouse fields, but employers add arbitrary required
	// questions per job. When an LLM is wired, read the live form and let
	// the model answer those extra fields from the candidate profile.
	// Best-effort: any failure here just submits with the static fields.
	// Static fields win over LLM answers so the model can never clobber
	// name/email/phone/cover-letter.
	if s.llm != nil {
		if fields, ferr := s.client.GetFormFields(ctx, req.ApplyURL); ferr == nil && len(fields) > 0 {
			answers, _ := autoapply.AnswerFieldsStructured(ctx, s.llm, fields, autoapply.FieldAnswerProfile{
				FullName:     req.FullName,
				Email:        req.Email,
				Phone:        req.Phone,
				Location:     req.Location,
				CurrentTitle: req.CurrentTitle,
				Skills:       req.Skills,
				CoverLetter:  req.CoverLetter,
			})
			for sel, val := range answers {
				if val == "" {
					continue
				}
				if _, taken := textFields[sel]; !taken {
					textFields[sel] = val
				}
			}

			// Required checkboxes are consent/acknowledgement gates (privacy
			// policy, accuracy attestation, …) that must be checked to submit.
			// The LLM frequently omits them, so default any required checkbox
			// to checked unless something already answered it.
			for _, f := range fields {
				if f.Type == "checkbox" && f.Required {
					if _, taken := textFields[f.Selector]; !taken {
						textFields[f.Selector] = "true"
					}
				}
			}
		}
	}

	opts := browser.SubmitOptions{
		URL:            req.ApplyURL,
		TextFields:     textFields,
		FileField:      "#resume, #job_application_resume",
		FileBytes:      req.CVBytes,
		FileName:       req.CVFilename,
		SubmitSel:      "[data-submit='true'], input[type='submit'], button[type='submit']",
		ConfirmText:    "thank you for applying",
		ScreenshotPath:     s.screenshotPath,
		NoSubmit:           s.fillOnly,
		PostSubmitShotPath: s.postSubmitShot,
	}

	// Wire the email-OTP hold-open path when a rendezvous is configured
	// and we can derive the company (the rendezvous key, shared with the
	// OTP-email ingress, is candidate email + board company).
	if s.rdv != nil && req.Email != "" {
		if company := otprendezvous.CompanyFromGreenhouseURL(req.ApplyURL); company != "" {
			key := otprendezvous.Key(req.Email, company)
			// Drop any stale code from a prior attempt so we only ever
			// consume the code emailed for THIS submission.
			_ = s.rdv.Clear(ctx, key)
			opts.OTPFieldSel = greenhouseOTPField
			opts.CodeProvider = func(c context.Context) (string, error) {
				return s.rdv.Poll(c, key, s.otpWait)
			}
		}
	}

	err := s.client.FillAndSubmit(ctx, opts)
	if err != nil {
		if errors.Is(err, browser.ErrCAPTCHA) {
			return autoapply.SubmitResult{Method: "skipped", SkipReason: "captcha"}, nil
		}
		if errors.Is(err, browser.ErrOTPTimeout) {
			return autoapply.SubmitResult{Method: "skipped", SkipReason: "otp_timeout"}, nil
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
