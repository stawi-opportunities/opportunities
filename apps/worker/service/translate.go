package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pitabwire/frame"
	"github.com/pitabwire/util"

	eventsv1 "github.com/stawi-opportunities/opportunities/pkg/events/v1"
	"github.com/stawi-opportunities/opportunities/pkg/extraction"
	"github.com/stawi-opportunities/opportunities/pkg/telemetry"
)

// TranslateHandler is a Frame Queue subscriber on
// SubjectWorkerTranslate. It receives a CanonicalUpsertedV1 envelope
// and emits one TranslationV1 per configured target language. If
// TranslationLangs is empty the handler is a no-op.
//
// Translation calls are external LLM I/O (Groq) that may take seconds
// and may fail. Per the Frame async decision tree these belong on a
// Queue with retry/backoff, not on the events bus. The dedup key is
// opportunity_id + lang + canonical_revision, so re-delivery is safe.
type TranslateHandler struct {
	svc       *frame.Service
	extractor *extraction.Extractor
	langs     []string
}

// NewTranslateHandler ...
func NewTranslateHandler(svc *frame.Service, ex *extraction.Extractor, langs []string) *TranslateHandler {
	return &TranslateHandler{svc: svc, extractor: ex, langs: langs}
}

// Handle implements queue.SubscribeWorker.
func (h *TranslateHandler) Handle(ctx context.Context, _ map[string]string, payload []byte) error {
	if len(h.langs) == 0 || h.extractor == nil {
		return nil
	}
	if len(payload) == 0 {
		return errors.New("translate: empty payload")
	}
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(payload, &env); err != nil {
		return err
	}
	c := env.Payload
	canonLang, _ := c.Attributes["language"].(string)
	for _, lang := range h.langs {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang == "" || lang == strings.ToLower(canonLang) {
			continue
		}
		tr, err := h.translate(ctx, c, lang)
		if err != nil {
			reason := classifyTranslateFailure(err)
			telemetry.RecordTranslateFailure(lang, reason)
			util.Log(ctx).WithError(err).
				WithField("lang", lang).
				WithField("reason", reason).
				Warn("translate: provider failed, skipping")
			continue
		}
		outEnv := eventsv1.NewEnvelope(eventsv1.TopicTranslations, tr)
		if err := h.svc.EventsManager().Emit(ctx, eventsv1.TopicTranslations, outEnv); err != nil {
			return err
		}
	}
	return nil
}

func (h *TranslateHandler) translate(ctx context.Context, c eventsv1.CanonicalUpsertedV1, lang string) (eventsv1.TranslationV1, error) {
	desc, _ := c.Attributes["description"].(string)
	system := fmt.Sprintf(`You are a translator. Translate the title and description into %s. Output ONLY JSON: {"title":"...","description":"..."}`, lang)
	user := "Title: " + c.Title + "\n\nDescription:\n" + desc
	raw, err := h.extractor.Prompt(ctx, system, user)
	if err != nil {
		return eventsv1.TranslationV1{}, err
	}
	var out struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return eventsv1.TranslationV1{}, fmt.Errorf("translate: parse: %w", err)
	}
	return eventsv1.TranslationV1{
		OpportunityID: c.OpportunityID,
		Lang:          lang,
		TitleTr:       out.Title,
		DescriptionTr: out.Description,
		TranslatedAt:  time.Now().UTC(),
	}, nil
}

// classifyTranslateFailure maps an error from the translation provider
// to a short, low-cardinality reason tag suitable for an OTel attribute.
func classifyTranslateFailure(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "rate limit"), strings.Contains(msg, "429"):
		return "rate_limit"
	case strings.Contains(msg, "parse"), strings.Contains(msg, "decode"), strings.Contains(msg, "unmarshal"):
		return "parse"
	case strings.Contains(msg, "context deadline"), strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "connection"), strings.Contains(msg, "network"), strings.Contains(msg, "no such host"):
		return "network"
	default:
		return "unknown"
	}
}
