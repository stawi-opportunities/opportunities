package service

// encode.go — one typed encode function per Parquet topic.
//
// Each function decodes the raw envelope JSON, copies EventID and
// OccurredAt from the envelope into the payload struct, then serialises
// the slice as Parquet via eventlog.WriteParquet.
//
// EncodeBatchVariantIngested is exported (capital E) so the round-trip
// test in encode_test.go can call it directly. All others are
// package-private and exercised through uploadBatch.

import (
	"encoding/json"
	"fmt"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// EncodeBatchVariantIngested is exported for test access.
func EncodeBatchVariantIngested(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.VariantIngestedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.VariantIngestedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchVariantNormalized(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.VariantNormalizedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.VariantNormalizedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchVariantValidated(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.VariantValidatedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.VariantValidatedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchVariantFlagged(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.VariantFlaggedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.VariantFlaggedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchVariantClustered(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.VariantClusteredV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.VariantClusteredV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchCanonicalUpserted(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.CanonicalUpsertedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.CanonicalUpsertedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchEmbedding(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.EmbeddingV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.EmbeddingV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchTranslation(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.TranslationV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.TranslationV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchPublished(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.PublishedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.PublishedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchCrawlPageCompleted(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.CrawlPageCompletedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.CrawlPageCompletedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchSourceDiscovered(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.SourceDiscoveredV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.SourceDiscoveredV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchCVUploaded(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.CVUploadedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.CVUploadedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchCVExtracted(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.CVExtractedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.CVExtractedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchCVImproved(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.CVImprovedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.CVImprovedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchCandidateEmbedding(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.CandidateEmbeddingV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.CandidateEmbeddingV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchPreferencesUpdated(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.PreferencesUpdatedV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.PreferencesUpdatedV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}

func encodeBatchMatchesReady(raws []json.RawMessage) ([]byte, error) {
	rows := make([]eventsv1.MatchesReadyV1, 0, len(raws))
	for _, r := range raws {
		var env eventsv1.Envelope[eventsv1.MatchesReadyV1]
		if err := json.Unmarshal(r, &env); err != nil {
			return nil, fmt.Errorf("writer: decode envelope: %w", err)
		}
		p := env.Payload
		p.EventID = env.EventID
		p.OccurredAt = env.OccurredAt
		rows = append(rows, p)
	}
	return eventlog.WriteParquet(rows)
}
