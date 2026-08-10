package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/stawi-opportunities/opportunities/pkg/ats"
)

func testMux(t *testing.T) http.Handler {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:http-"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := ats.NewStore(db)
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	svc := ats.NewService(store)
	mux := http.NewServeMux()
	Mount(mux, &Deps{Svc: svc, Auth: TenancyAuth(nil, true)})
	return mux
}

func withTenancy(req *http.Request) *http.Request {
	req.Header.Set("X-Profile-ID", "rec-1")
	req.Header.Set("X-Tenant-ID", "t1")
	req.Header.Set("X-Partition-ID", "p1")
	return req
}

func TestHTTPCreateJobAndPipeline(t *testing.T) {
	mux := testMux(t)

	body := bytes.NewBufferString(`{"title":"Backend","status":"open"}`)
	req := withTenancy(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create job %d %s", rr.Code, rr.Body.String())
	}
	var job ats.Job
	if err := json.Unmarshal(rr.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}

	body = bytes.NewBufferString(`{"profile_id":"cand-9"}`)
	req = withTenancy(httptest.NewRequest(http.MethodPost, "/v1/jobs/"+job.ID+"/applications", body))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create app %d %s", rr.Code, rr.Body.String())
	}
	var app ats.Application
	_ = json.Unmarshal(rr.Body.Bytes(), &app)

	body = bytes.NewBufferString(`{"to_stage":"screen"}`)
	req = withTenancy(httptest.NewRequest(http.MethodPost, "/v1/applications/"+app.ID+"/advance", body))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("advance %d %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPUnauthorized(t *testing.T) {
	mux := testMux(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rr.Code)
	}
}
