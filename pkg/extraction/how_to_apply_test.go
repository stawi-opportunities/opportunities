package extraction

import (
	"context"
	"strings"
	"testing"
)

type peelLLM struct {
	response string
	err      error
	calls    int
	last     string
}

func (f *peelLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.calls++
	f.last = prompt
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func TestPeelHowToApply_Success(t *testing.T) {
	llm := &peelLLM{response: `{
		"description": "## Role\n\nBuild APIs.",
		"how_to_apply": "Email careers@acme.test with your CV."
	}`}
	e := &Extractor{llm: llm}
	desc := strings.Repeat("We need a strong engineer to build systems. ", 10) +
		"\n\n## How to Apply\n\nEmail careers@acme.test with your CV."
	clean, how, err := e.PeelHowToApply(context.Background(), "Engineer", "job", desc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clean, "Build APIs") {
		t.Fatalf("clean=%q", clean)
	}
	if !strings.Contains(how, "careers@acme.test") {
		t.Fatalf("how=%q", how)
	}
	if llm.calls != 1 {
		t.Fatalf("calls=%d", llm.calls)
	}
	if !strings.Contains(llm.last, "kind: job") {
		t.Fatalf("prompt missing kind: %q", llm.last)
	}
}

func TestPeelHowToApply_ShortSkipsLLM(t *testing.T) {
	llm := &peelLLM{response: `{"description":"x","how_to_apply":"y"}`}
	e := &Extractor{llm: llm}
	clean, how, err := e.PeelHowToApply(context.Background(), "T", "job", "short")
	if err != nil {
		t.Fatal(err)
	}
	if clean != "short" || how != "" {
		t.Fatalf("got %q %q", clean, how)
	}
	if llm.calls != 0 {
		t.Fatalf("LLM should not run for short body, calls=%d", llm.calls)
	}
}

func TestPeelHowToApply_EmptyHow(t *testing.T) {
	llm := &peelLLM{response: `{"description":"Full role details without apply steps here enough length now.","how_to_apply":""}`}
	e := &Extractor{llm: llm}
	body := strings.Repeat("Full role details without apply steps here. ", 8)
	clean, how, err := e.PeelHowToApply(context.Background(), "Role", "job", body)
	if err != nil {
		t.Fatal(err)
	}
	if how != "" {
		t.Fatalf("how=%q", how)
	}
	if clean == "" {
		t.Fatal("empty clean")
	}
}

func TestPeelHowToApply_RefusesBlankDescription(t *testing.T) {
	llm := &peelLLM{response: `{"description":"","how_to_apply":"Email x@y.z"}`}
	e := &Extractor{llm: llm}
	body := strings.Repeat("Keep this public body when the model blanks description. ", 5)
	clean, how, err := e.PeelHowToApply(context.Background(), "Role", "job", body)
	if err != nil {
		t.Fatal(err)
	}
	if clean != strings.TrimSpace(body) {
		t.Fatalf("should keep original body, got %q", clean)
	}
	if how != "Email x@y.z" {
		t.Fatalf("how=%q", how)
	}
}

func TestParseHowToApplySplit_Fences(t *testing.T) {
	raw := "```json\n{\"description\":\"A\",\"how_to_apply\":\"B\"}\n```"
	s, err := parseHowToApplySplit(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Description != "A" || s.HowToApply != "B" {
		t.Fatalf("%+v", s)
	}
}

func TestBalanceTruncate_KeepsTail(t *testing.T) {
	head := strings.Repeat("H", 4000)
	// Marker at the very end so a 40% tail window must retain it.
	tail := strings.Repeat("T", 500) + "APPLY_TAIL_MARKER"
	out := balanceTruncate(head+tail, 1000)
	if !strings.Contains(out, "APPLY_TAIL_MARKER") {
		t.Fatalf("tail lost: %q", out[len(out)-80:])
	}
	if !strings.Contains(out, "H") {
		t.Fatal("head lost")
	}
}
