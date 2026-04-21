package service_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"

	writersvc "stawi.jobs/apps/writer/service"
)

func TestEncode_PreservesEnvelopeMeta(t *testing.T) {
	env := eventsv1.NewEnvelope(eventsv1.TopicVariantsIngested, eventsv1.VariantIngestedV1{
		VariantID: "var-1", SourceID: "acme", HardKey: "hk-1",
	})
	raw, err := json.Marshal(env)
	require.NoError(t, err)

	body, err := writersvc.EncodeBatchVariantIngested([]json.RawMessage{raw})
	require.NoError(t, err)

	rows, err := eventlog.ReadParquet[eventsv1.VariantIngestedV1](body)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, env.EventID, rows[0].EventID, "EventID survives round-trip")
	require.Equal(t, env.OccurredAt.UTC(), rows[0].OccurredAt.UTC(), "OccurredAt survives round-trip")
	require.Equal(t, "var-1", rows[0].VariantID)
}
