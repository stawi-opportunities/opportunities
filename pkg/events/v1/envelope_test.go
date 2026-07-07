package eventsv1

import (
	"encoding/json"
	"testing"
)

func TestVariantEnvelopeRoundTrip(t *testing.T) {
	original := NewEnvelope(TopicVariantsIngested, VariantIngestedV1{VariantID: "v1", SourceID: "s1", HardKey: "s1|x", Kind: "job", Title: "Engineer"})
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Envelope[VariantIngestedV1]
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Payload.VariantID != "v1" || decoded.Payload.Title != "Engineer" {
		t.Fatalf("unexpected envelope: %#v", decoded)
	}
}
