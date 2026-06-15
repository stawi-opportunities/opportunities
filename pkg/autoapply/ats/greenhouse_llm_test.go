package ats

import (
	"context"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
)

type fakeLLM struct{ reply string }

func (f *fakeLLM) Complete(_ context.Context, _, _ string) (string, error) {
	return f.reply, nil
}

func TestGreenhouseLLMAnswersCustomQuestions(t *testing.T) {
	fc := &fakeApplyClient{
		fields: []browser.FieldDescriptor{
			{Selector: "#work_auth", Label: "Work authorization", Type: "select", Options: []string{"Yes", "No"}},
			{Selector: "#email", Label: "Email", Type: "email"},
		},
	}
	// The model answers a custom question and (wrongly) tries to set email;
	// the static field must win for #email, and the custom answer must land.
	llm := &fakeLLM{reply: `{"#work_auth":"Yes","#email":"hacked@x.com"}`}

	s := NewGreenhouseSubmitter(fc).WithLLM(llm)
	if _, err := s.Submit(context.Background(), ghRequest()); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if fc.lastOpts.TextFields["#work_auth"] != "Yes" {
		t.Fatalf("custom answer not filled: %#v", fc.lastOpts.TextFields)
	}
	if fc.lastOpts.TextFields["#email"] != "joakim@example.com" {
		t.Fatalf("LLM overrode a static field: #email=%q", fc.lastOpts.TextFields["#email"])
	}
}

func TestGreenhouseNoLLMLeavesFormStatic(t *testing.T) {
	fc := &fakeApplyClient{
		fields: []browser.FieldDescriptor{{Selector: "#work_auth", Label: "Work authorization", Type: "select"}},
	}
	s := NewGreenhouseSubmitter(fc) // no LLM

	if _, err := s.Submit(context.Background(), ghRequest()); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, ok := fc.lastOpts.TextFields["#work_auth"]; ok {
		t.Fatal("custom field should not be filled without an LLM")
	}
}

var _ autoapply.LLMClient = (*fakeLLM)(nil)
