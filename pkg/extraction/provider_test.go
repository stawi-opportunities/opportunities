package extraction

import "testing"

func TestResolveInference_Providers(t *testing.T) {
	u, m, k := ResolveInference("google", "", "", "key")
	if u != GoogleAIBaseURL || m != GoogleAIChatModel || k != "key" {
		t.Fatalf("google defaults: url=%q model=%q key=%q", u, m, k)
	}
	u, m, _ = ResolveInference("nvidia", "", "", "nv")
	if u != NVIDIABuildBaseURL || m != NVIDIABuildChatModel {
		t.Fatalf("nvidia defaults: url=%q model=%q", u, m)
	}
	// Explicit URL wins over provider defaults for model only when set.
	u, m, _ = ResolveInference("nvidia", "https://custom.example", "my-model", "nv")
	if u != "https://custom.example" || m != "my-model" {
		t.Fatalf("explicit override: url=%q model=%q", u, m)
	}
}

func TestResolveEmbedding_Google(t *testing.T) {
	u, m, k := ResolveEmbedding("google", "", "", "key")
	if u != GoogleAIBaseURL || m != GoogleAIEmbedModel || k != "key" {
		t.Fatalf("google embed: url=%q model=%q key=%q", u, m, k)
	}
}

func TestResolveEmbedding_NVIDIA_MultilingualDefault(t *testing.T) {
	u, m, k := ResolveEmbedding("nvidia", "", "", "nvapi-x")
	if u != NVIDIABuildBaseURL {
		t.Fatalf("url: got %q want %q", u, NVIDIABuildBaseURL)
	}
	if m != "nvidia/llama-nemotron-embed-1b-v2" {
		t.Fatalf("model: got %q want multilingual llama-nemotron-embed-1b-v2", m)
	}
	if k != "nvapi-x" {
		t.Fatalf("key: got %q", k)
	}
}
