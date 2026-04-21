package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/pitabwire/util"
)

// CompactHourlyHandler returns an http.HandlerFunc bound to the given
// compactor. Expects JSON body {"collection":"canonicals","hour":"..."}.
// hour is optional — defaults to now.
func CompactHourlyHandler(c *Compactor) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		var body struct {
			Collection string    `json:"collection"`
			Hour       time.Time `json:"hour,omitempty"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if body.Collection == "" {
			http.Error(w, `{"error":"collection required"}`, http.StatusBadRequest)
			return
		}
		if body.Hour.IsZero() {
			body.Hour = time.Now().UTC()
		}

		res, err := c.CompactHourly(ctx, CompactHourlyInput{
			Collection: body.Collection,
			Hour:       body.Hour,
		})
		if err != nil {
			util.Log(ctx).WithError(err).WithField("collection", body.Collection).
				Error("compact hourly failed")
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "unknown collection") {
				status = http.StatusBadRequest
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, status)
			return
		}

		util.Log(ctx).
			WithField("collection", body.Collection).
			WithField("rows_before", res.RowsBefore).
			WithField("rows_after", res.RowsAfter).
			WithField("files_before", res.FilesBefore).
			WithField("files_after", res.FilesAfter).
			Info("compact hourly done")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}

// CompactDailyHandler returns an http.HandlerFunc bound to the given
// compactor. Expects JSON body {"collection":"canonicals"}.
func CompactDailyHandler(c *Compactor) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		var body struct {
			Collection string `json:"collection"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"invalid body"}`, http.StatusBadRequest)
			return
		}
		if body.Collection == "" {
			http.Error(w, `{"error":"collection required"}`, http.StatusBadRequest)
			return
		}

		res, err := c.CompactDaily(ctx, CompactDailyInput{Collection: body.Collection})
		if err != nil {
			util.Log(ctx).WithError(err).WithField("collection", body.Collection).
				Error("compact daily failed")
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "has no _current partition") ||
				strings.Contains(err.Error(), "unknown collection") {
				status = http.StatusBadRequest
			}
			http.Error(w, `{"error":"`+err.Error()+`"}`, status)
			return
		}

		util.Log(ctx).
			WithField("collection", body.Collection).
			WithField("rows_before", res.RowsBefore).
			WithField("rows_after", res.RowsAfter).
			WithField("buckets", res.Buckets).
			Info("compact daily done")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	}
}
