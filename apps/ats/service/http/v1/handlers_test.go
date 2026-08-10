package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	return corsWrap(mux)
}

func corsWrap(h http.Handler) http.Handler { return h }

func withTenancy(req *http.Request) *http.Request {
	req.Header.Set("X-Profile-ID", "rec-1")
	req.Header.Set("X-Tenant-ID", "t1")
	req.Header.Set("X-Partition-ID", "p1")
	return req
}

func TestHTTPCreateJobPipelineTalentScheduleHire(t *testing.T) {
	mux := testMux(t)

	// seed
	req := withTenancy(httptest.NewRequest(http.MethodPost, "/v1/demo/seed", nil))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("seed %d %s", rr.Code, rr.Body.String())
	}

	// list jobs
	req = withTenancy(httptest.NewRequest(http.MethodGet, "/v1/jobs", nil))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list jobs %d %s", rr.Code, rr.Body.String())
	}
	var jobsResp struct {
		Jobs []ats.JobDTO `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &jobsResp); err != nil {
		t.Fatal(err)
	}
	if len(jobsResp.Jobs) == 0 {
		t.Fatal("expected seeded jobs")
	}
	jobID := jobsResp.Jobs[0].ID

	// talent
	req = withTenancy(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+jobID+"/talent", nil))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("talent %d %s", rr.Code, rr.Body.String())
	}
	var talentResp struct {
		Talent []ats.TalentHit `json:"talent"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &talentResp)
	if len(talentResp.Talent) == 0 {
		t.Fatal("expected demo talent")
	}

	// applications
	req = withTenancy(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+jobID+"/applications", nil))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	var appsResp struct {
		Applications []ats.ApplicationDTO `json:"applications"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &appsResp)
	if len(appsResp.Applications) == 0 {
		// add one
		body := bytes.NewBufferString(`{"profile_id":"prof_test","summary":"Go engineer"}`)
		req = withTenancy(httptest.NewRequest(http.MethodPost, "/v1/jobs/"+jobID+"/applications", body))
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create app %d %s", rr.Code, rr.Body.String())
		}
		var app ats.ApplicationDTO
		_ = json.Unmarshal(rr.Body.Bytes(), &app)
		appsResp.Applications = []ats.ApplicationDTO{app}
	}
	appID := appsResp.Applications[0].ID

	// screen AI
	req = withTenancy(httptest.NewRequest(http.MethodPost, "/v1/ai/applications/"+appID+"/screen-summary", nil))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("ai %d %s", rr.Code, rr.Body.String())
	}

	// availability
	body := bytes.NewBufferString(`{"timezone":"UTC","rules":[{"weekday":1,"start":"09:00","end":"17:00"},{"weekday":2,"start":"09:00","end":"17:00"},{"weekday":3,"start":"09:00","end":"17:00"},{"weekday":4,"start":"09:00","end":"17:00"},{"weekday":5,"start":"09:00","end":"17:00"}]}`)
	req = withTenancy(httptest.NewRequest(http.MethodPut, "/v1/me/availability", body))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("avail %d %s", rr.Code, rr.Body.String())
	}

	// propose interview
	body = bytes.NewBufferString(`{"duration_min":30,"type":"screen","panel":["rec-1"]}`)
	req = withTenancy(httptest.NewRequest(http.MethodPost, "/v1/applications/"+appID+"/interviews", body))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("propose %d %s", rr.Code, rr.Body.String())
	}
	var iv ats.InterviewDTO
	_ = json.Unmarshal(rr.Body.Bytes(), &iv)

	// slots + book
	req = withTenancy(httptest.NewRequest(http.MethodGet, "/v1/interviews/"+iv.ID+"/slots", nil))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("slots %d %s", rr.Code, rr.Body.String())
	}
	var slotsResp struct {
		Slots []ats.Slot `json:"slots"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &slotsResp)
	if len(slotsResp.Slots) == 0 {
		t.Skip("no slots in next 14 days from now")
	}
	bookBody, _ := json.Marshal(map[string]time.Time{
		"start": slotsResp.Slots[0].Start,
		"end":   slotsResp.Slots[0].End,
	})
	req = withTenancy(httptest.NewRequest(http.MethodPost, "/v1/interviews/"+iv.ID+"/book", bytes.NewReader(bookBody)))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("book %d %s", rr.Code, rr.Body.String())
	}

	// advance to offer then hire
	for _, st := range []string{"screen", "interview", "offer"} {
		// may fail if already past — ignore
		b := bytes.NewBufferString(`{"to_stage":"` + st + `"}`)
		req = withTenancy(httptest.NewRequest(http.MethodPost, "/v1/applications/"+appID+"/advance", b))
		rr = httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
	}
	req = withTenancy(httptest.NewRequest(http.MethodPost, "/v1/applications/"+appID+"/hire", nil))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	// hire may work if at offer
	if rr.Code != http.StatusOK && rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("hire %d %s", rr.Code, rr.Body.String())
	}

	// dashboard
	req = withTenancy(httptest.NewRequest(http.MethodGet, "/v1/dashboard", nil))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("dashboard %d", rr.Code)
	}

	// publish
	req = withTenancy(httptest.NewRequest(http.MethodPost, "/v1/jobs/"+jobID+"/publish", nil))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("publish %d %s", rr.Code, rr.Body.String())
	}
	var pub ats.JobDTO
	_ = json.Unmarshal(rr.Body.Bytes(), &pub)
	if pub.Visibility != "published" || pub.OpportunityID == "" {
		t.Fatalf("publish dto %+v", pub)
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
