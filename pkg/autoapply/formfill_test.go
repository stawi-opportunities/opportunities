package autoapply

import (
	"context"
	"errors"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply/browser"
)

type fakeLLM struct {
	reply   string
	err     error
	gotUser string
}

func (f *fakeLLM) Complete(_ context.Context, _, user string) (string, error) {
	f.gotUser = user
	return f.reply, f.err
}

const appFormHTML = `<html><body>
<form id="application">
  <input name="first_name"/>
  <label>Are you authorized to work in the US?</label>
  <select id="auth"><option>Yes</option><option>No</option></select>
  <textarea id="why">Why do you want to work here?</textarea>
</form>
</body></html>`

func TestAnswerFormFields(t *testing.T) {
	llm := &fakeLLM{reply: `{"#auth":"Yes","#why":"I admire the mission."}`}
	got, err := AnswerFormFields(context.Background(), llm, appFormHTML, FieldAnswerProfile{FullName: "A B"})
	if err != nil {
		t.Fatalf("AnswerFormFields: %v", err)
	}
	if got["#auth"] != "Yes" || got["#why"] != "I admire the mission." {
		t.Fatalf("unexpected answers: %#v", got)
	}
}

func TestAnswerFormFieldsNilLLM(t *testing.T) {
	got, err := AnswerFormFields(context.Background(), nil, appFormHTML, FieldAnswerProfile{})
	if err != nil || len(got) != 0 {
		t.Fatalf("nil llm: got %#v err %v, want empty map", got, err)
	}
}

func TestAnswerFormFieldsNoForm(t *testing.T) {
	llm := &fakeLLM{reply: `{"x":"y"}`}
	got, err := AnswerFormFields(context.Background(), llm, "<html><body>no form here</body></html>", FieldAnswerProfile{})
	if err != nil || len(got) != 0 {
		t.Fatalf("no form: got %#v err %v, want empty map", got, err)
	}
}

func TestAnswerFormFieldsLLMError(t *testing.T) {
	llm := &fakeLLM{err: errors.New("boom")}
	if _, err := AnswerFormFields(context.Background(), llm, appFormHTML, FieldAnswerProfile{}); err == nil {
		t.Fatal("expected error from failed LLM call")
	}
}

func TestAnswerFieldsStructuredDropsHallucinated(t *testing.T) {
	fields := []browser.FieldDescriptor{
		{Selector: "#question_1", Label: "Work authorization?", Type: "select", Options: []string{"Yes", "No"}},
		{Selector: "#question_2", Label: "Country of residence", Type: "text"},
	}
	// Model returns: a real answer, a hallucinated selector, and an empty value.
	llm := &fakeLLM{reply: `{"#question_1":"No","#made_up":"x","#question_2":""}`}

	got, err := AnswerFieldsStructured(context.Background(), llm, fields, FieldAnswerProfile{})
	if err != nil {
		t.Fatalf("AnswerFieldsStructured: %v", err)
	}
	if len(got) != 1 || got["#question_1"] != "No" {
		t.Fatalf("expected only #question_1=No, got %#v", got)
	}
}

func TestAnswerFieldsStructuredNoFields(t *testing.T) {
	got, err := AnswerFieldsStructured(context.Background(), &fakeLLM{reply: `{}`}, nil, FieldAnswerProfile{})
	if err != nil || len(got) != 0 {
		t.Fatalf("no fields: got %#v err %v", got, err)
	}
}

func TestAnswerFormFieldsUnparseable(t *testing.T) {
	// A non-JSON reply is treated as "no confident answers", not an error.
	llm := &fakeLLM{reply: "I cannot help with that."}
	got, err := AnswerFormFields(context.Background(), llm, appFormHTML, FieldAnswerProfile{})
	if err != nil || len(got) != 0 {
		t.Fatalf("unparseable: got %#v err %v, want empty map", got, err)
	}
}
