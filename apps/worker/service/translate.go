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
)

// TranslateHandler consumes CanonicalUpsertedV1 and emits one
// TranslationV1 per configured target language. If TranslationLangs
// is empty the handler is a no-op.
type TranslateHandler struct {
	svc       *frame.Service
	extractor *extraction.Extractor
	langs     []string
}

// NewTranslateHandler ...
func NewTranslateHandler(svc *frame.Service, ex *extraction.Extractor, langs []string) *TranslateHandler {
	return &TranslateHandler{svc: svc, extractor: ex, langs: langs}
}

// Name returns the topic this handler consumes (canonical upserts).
func (h *TranslateHandler) Name() string { return eventsv1.TopicCanonicalsUpserted }

// PayloadType ...
func (h *TranslateHandler) PayloadType() any {
	var raw json.RawMessage
	return &raw
}

// Validate ...
func (h *TranslateHandler) Validate(_ context.Context, payload any) error {
	raw, ok := payload.(*json.RawMessage)
	if !ok || raw == nil || len(*raw) == 0 {
		return errors.New("translate: empty payload")
	}
	return nil
}

// Execute translates into each configured target lang. Skips langs
// already matching the canonical's own language.
func (h *TranslateHandler) Execute(ctx context.Context, payload any) error {
	if len(h.langs) == 0 || h.extractor == nil {
		return nil
	}
	raw := payload.(*json.RawMessage)
	var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
	if err := json.Unmarshal(*raw, &env); err != nil {
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
			util.Log(ctx).WithError(err).WithField("lang", lang).
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
