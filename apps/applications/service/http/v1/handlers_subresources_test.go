//go:build integration

package v1_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNotesHandlers_CreatePatchDelete(t *testing.T) {
	mux, _, _, _, _ := setupHandlerEnv(t)
	w := doReq(t, mux, "POST", "/api/me/applications", map[string]any{"match_id": "m_n1", "opportunity_id": "o_n1"}, "c_n", "")
	require.Equal(t, http.StatusCreated, w.Code)
	var app map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &app)
	appID, _ := app["application_id"].(string)

	w1 := doReq(t, mux, "POST", "/api/me/applications/"+appID+"/notes",
		map[string]any{"body": "hello"}, "c_n", "k1")
	require.Equal(t, http.StatusCreated, w1.Code, "body=%s", w1.Body.String())
	var n1 map[string]any
	_ = json.Unmarshal(w1.Body.Bytes(), &n1)
	noteID, _ := n1["note_id"].(string)

	w2 := doReq(t, mux, "PATCH", "/api/me/applications/"+appID+"/notes/"+noteID,
		map[string]any{"body": "world"}, "c_n", "")
	require.Equal(t, http.StatusOK, w2.Code)

	w3 := doReq(t, mux, "DELETE", "/api/me/applications/"+appID+"/notes/"+noteID, nil, "c_n", "")
	require.Equal(t, http.StatusNoContent, w3.Code)
}

func TestRemindersHandlers_CreateMarkDoneList(t *testing.T) {
	mux, _, _, _, _ := setupHandlerEnv(t)
	w := doReq(t, mux, "POST", "/api/me/applications",
		map[string]any{"match_id": "m_r1", "opportunity_id": "o_r1"}, "c_r", "")
	require.Equal(t, http.StatusCreated, w.Code)
	var app map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &app)
	appID, _ := app["application_id"].(string)

	w1 := doReq(t, mux, "POST", "/api/me/applications/"+appID+"/reminders",
		map[string]any{"due_at": time.Now().Add(48 * time.Hour).Format(time.RFC3339), "note": "follow up"},
		"c_r", "k-rem-1")
	require.Equal(t, http.StatusCreated, w1.Code, "body=%s", w1.Body.String())
	var rem map[string]any
	_ = json.Unmarshal(w1.Body.Bytes(), &rem)
	rid, _ := rem["reminder_id"].(string)

	// List
	w2 := doReq(t, mux, "GET", "/api/me/reminders", nil, "c_r", "")
	require.Equal(t, http.StatusOK, w2.Code)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &listResp)
	require.Len(t, listResp.Items, 1)

	// Mark done
	w3 := doReq(t, mux, "PATCH", "/api/me/applications/"+appID+"/reminders/"+rid,
		map[string]any{"status": "done"}, "c_r", "")
	require.Equal(t, http.StatusOK, w3.Code)

	// Delete
	w4 := doReq(t, mux, "DELETE", "/api/me/applications/"+appID+"/reminders/"+rid, nil, "c_r", "")
	require.Equal(t, http.StatusNoContent, w4.Code)
}

func TestAttachmentsHandlers_UploadPresignDelete(t *testing.T) {
	mux, _, _, _, _ := setupHandlerEnv(t)
	w := doReq(t, mux, "POST", "/api/me/applications",
		map[string]any{"match_id": "m_a1", "opportunity_id": "o_a1"}, "c_a", "")
	require.Equal(t, http.StatusCreated, w.Code)
	var app map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &app)
	appID, _ := app["application_id"].(string)

	// Multipart upload
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("file", "cv.pdf")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("%PDF-1.7\nfake-pdf-bytes"))
	mw.Close()

	r := httptest.NewRequest("POST", "/api/me/applications/"+appID+"/attachments", body)
	r.Header.Set("X-Candidate-ID", "c_a")
	r.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())
	var att map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &att)
	attID, _ := att["attachment_id"].(string)
	require.NotEmpty(t, attID)

	// Presign
	w2 := doReq(t, mux, "GET", "/api/me/attachments/"+attID, nil, "c_a", "")
	require.Equal(t, http.StatusOK, w2.Code)
	var pres map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &pres)
	url, _ := pres["download_url"].(string)
	require.Contains(t, url, "memory://applications/c_a/"+appID+"/")

	// Delete
	w3 := doReq(t, mux, "DELETE", "/api/me/applications/"+appID+"/attachments/"+attID, nil, "c_a", "")
	require.Equal(t, http.StatusNoContent, w3.Code)
}
