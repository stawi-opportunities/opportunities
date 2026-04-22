package service

// encode.go — legacy Parquet encode helpers, retained for Wave 3
// materializer / compactor read-side use.
//
// The PRIMARY write path is now Iceberg-native (see arrow_build.go +
// service.go:commitBatch). These functions are NOT called by the writer
// flush loop any more.
//
// EncodeBatchVariantIngested remains exported because the existing
// round-trip test (encode_test.go) exercises the Parquet codec
// independently. That test is preserved under its current build tag
// (no tag = always built) so CI keeps verifying the Parquet codec
// that Wave 3 readers will consume.

import (
	"encoding/json"
	"fmt"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

// EncodeBatchVariantIngested serialises a batch of VariantIngestedV1
// envelopes as Parquet bytes. Exported for the round-trip unit test.
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
