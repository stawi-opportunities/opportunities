//go:build integration

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eventsv1 "stawi.jobs/pkg/events/v1"
	"stawi.jobs/pkg/eventlog"
)

func TestCompactHourlyHandler_OK(t *testing.T) {
	ctx := context.Background()
	mch := startMinIO(t)
	defer mch.Close()

	uploader := eventlog.NewUploader(mch.Client, mch.Bucket)
	reader := eventlog.NewReader(mch.Client, mch.Bucket)

	hour := time.Now().UTC().Truncate(time.Hour)
	body, _ := eventlog.WriteParquet([]eventsv1.VariantIngestedV1{
		{EventID: "v1", VariantID: "var-1", SourceID: "acme", OccurredAt: hour},
	})
	_, err := uploader.Put(ctx, "variants/dt="+hour.Format("2006-01-02")+"/src=acme/part-a.parquet", body)
	require.NoError(t, err)

	c := NewCompactor(mch.Client, reader, uploader, mch.Bucket)
	h := CompactHourlyHandler(c)

	reqBody, _ := json.Marshal(map[string]any{"collection": "variants"})
	req := httptest.NewRequest(http.MethodPost, "/_admin/compact/hourly", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp CompactHourlyResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Equal(t, 1, resp.RowsBefore)
	require.Equal(t, 1, resp.RowsAfter)
}

func TestCompactDailyHandler_UnknownCollection(t *testing.T) {
	mch := startMinIO(t)
	defer mch.Close()

	c := NewCompactor(mch.Client,
		eventlog.NewReader(mch.Client, mch.Bucket),
		eventlog.NewUploader(mch.Client, mch.Bucket),
		mch.Bucket,
	)
	h := CompactDailyHandler(c)

	reqBody, _ := json.Marshal(map[string]any{"collection": "variants"}) // no _current
	req := httptest.NewRequest(http.MethodPost, "/_admin/compact/daily", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	h(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}
