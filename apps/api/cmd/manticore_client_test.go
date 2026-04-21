package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"stawi.jobs/pkg/searchindex"
)

func TestJobsManticore_GetByID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"total":1,"hits":[{"_id":42,"_score":1,"_source":{"canonical_id":"can-1","slug":"acme-engineer","title":"Engineer","company":"Acme","country":"KE","remote_type":"remote","category":"engineering"}}]}}`))
	}))
	defer ts.Close()

	client, err := searchindex.Open(searchindex.Config{URL: ts.URL})
	require.NoError(t, err)
	jm := newJobsManticore(client)

	job, err := jm.GetByID(context.Background(), "can-1")
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, "acme-engineer", job.Slug)
	require.Equal(t, "Engineer", job.Title)
}

func TestJobsManticore_Count(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"total":1234,"hits":[]}}`))
	}))
	defer ts.Close()

	client, _ := searchindex.Open(searchindex.Config{URL: ts.URL})
	jm := newJobsManticore(client)

	n, err := jm.Count(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 1234, n)
}
