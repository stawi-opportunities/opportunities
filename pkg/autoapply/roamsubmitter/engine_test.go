package roamsubmitter

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stawi-opportunities/opportunities/pkg/autoapply"
	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestCanHandle(t *testing.T) {
	bmKE := New(BrighterMondayKE(), Config{})
	bmUG := New(BrighterMondayUG(), Config{})
	jbNG := New(JobbermanNG(), Config{})
	jbGH := New(JobbermanGH(), Config{})

	// Each handles its own source + /listings/ URL on its own origin.
	assert.True(t, bmKE.CanHandle(domain.SourceBrighterMonday,
		"https://www.brightermonday.co.ke/listings/oracle-apps-dba-45jq74"))
	assert.True(t, bmUG.CanHandle(domain.SourceBrighterMondayUG,
		"https://www.brightermonday.co.ug/listings/sales-lead-9xk2"))
	assert.True(t, jbNG.CanHandle(domain.SourceJobberman,
		"https://www.jobberman.com/listings/senior-fullstack-engineer-er95n9?ref=email"))
	assert.True(t, jbGH.CanHandle(domain.SourceJobbermanGH,
		"https://www.jobberman.com.gh/listings/data-analyst-7y3"))

	// Wrong source type (right brand, wrong country).
	assert.False(t, bmKE.CanHandle(domain.SourceBrighterMondayUG,
		"https://www.brightermonday.co.ke/listings/oracle-apps-dba-45jq74"))

	// Right source but the other country's / brand's origin.
	assert.False(t, bmUG.CanHandle(domain.SourceBrighterMondayUG,
		"https://www.brightermonday.co.ke/listings/oracle-apps-dba-45jq74"))
	assert.False(t, jbGH.CanHandle(domain.SourceJobbermanGH,
		"https://www.jobberman.com/listings/senior-fullstack-engineer-er95n9"))
}

func TestExtractListingSlug(t *testing.T) {
	cases := map[string]string{
		"https://www.jobberman.com/listings/senior-fullstack-engineer-er95n9":         "senior-fullstack-engineer-er95n9",
		"https://www.brightermonday.co.ke/listings/oracle-apps-dba-45jq74?ref=x":      "oracle-apps-dba-45jq74",
		"https://www.jobberman.com/listings/senior-fullstack-engineer-er95n9#section": "senior-fullstack-engineer-er95n9",
		"https://www.jobberman.com/account/customer/enquiries":                        "",
		"": "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			assert.Equal(t, want, extractListingSlug(in))
		})
	}
}

func TestExtractEnquiryForm_ProfileGuard(t *testing.T) {
	// Complete profile: every required field carries a value.
	complete := []byte(`
	  <form id="enquiry-form" action="https://www.jobberman.com/apply/store">
	    <input type="hidden" name="listing_id" value="42">
	    <input type="text" name="headline" value="Senior Engineer" required="required">
	    <input type="number" name="salary_expectation" value="500000" required="required">
	    <select name="work_type_id" required="required">
	      <option value="2" selected>Full Time</option>
	    </select>
	    <textarea name="description" required="required"></textarea>
	  </form>`)
	form, err := extractEnquiryForm(complete)
	assert.NoError(t, err)
	form.Fields["description"] = "cover letter" // engine sets this before the guard
	assert.Empty(t, form.missingRequired(), "no required field flagged when all are filled")
	assert.Equal(t, "42", form.ListingID)

	// Incomplete profile: required headline + select empty. `description`
	// is empty too but must NOT be flagged — the engine fills it.
	incomplete := []byte(`
	  <form id="enquiry-form" action="https://www.jobberman.com/apply/store">
	    <input type="text" name="headline" value="" required="required">
	    <input type="number" name="salary_expectation" value="500000" required="required">
	    <select name="qualification_level_id" data-rules="[&quot;required&quot;]">
	      <option value="" selected></option>
	    </select>
	    <textarea name="description" required="required"></textarea>
	    <input type="text" name="optional_field" value="">
	  </form>`)
	form, err = extractEnquiryForm(incomplete)
	assert.NoError(t, err)
	form.Fields["description"] = "cover letter" // engine sets this before the guard
	assert.ElementsMatch(t, []string{"headline", "qualification_level_id"}, form.missingRequired())
}

func TestFinalizeFields(t *testing.T) {
	// CV resolution: an uploaded CV present → select it the browser's way.
	// Currency: pulled from the Alpine multiSelect default (the id sits
	// between unicode-escaped quotes exactly as the live HTML renders it).
	body := []byte(`x-data="multiSelect(JSON.parse('[{}]'), JSON.parse('["116"]'), 1, 'currency_id', 1, null)"`)
	form := &Form{Fields: map[string]string{
		"uploaded_cv":        "10407192",
		"current_cv":         "",
		"resume_id":          "",
		"currency_id":        "",
		"salary_expectation": "",
	}}
	finalizeFields(form, body, autoapply.SubmitRequest{SalaryMin: 600000, SalaryMax: 800000})
	assert.Equal(t, "1", form.Fields["current_cv"])
	assert.Equal(t, "10407192", form.Fields["resume_id"])
	assert.Equal(t, "116", form.Fields["currency_id"])
	assert.Equal(t, "800000", form.Fields["salary_expectation"], "prefers the upper bound")

	// SalaryMax==0 falls back to SalaryMin.
	form2 := &Form{Fields: map[string]string{"salary_expectation": ""}}
	finalizeFields(form2, nil, autoapply.SubmitRequest{SalaryMin: 500000})
	assert.Equal(t, "500000", form2.Fields["salary_expectation"])

	// Already-populated fields are left untouched (idempotent for the
	// complete-profile / server-pre-filled case); no salary on file leaves
	// it empty for the guard to catch.
	form3 := &Form{Fields: map[string]string{
		"uploaded_cv":        "55",
		"current_cv":         "2",
		"resume_id":          "99",
		"currency_id":        "44",
		"salary_expectation": "123",
	}}
	finalizeFields(form3, nil, autoapply.SubmitRequest{})
	assert.Equal(t, "2", form3.Fields["current_cv"])
	assert.Equal(t, "99", form3.Fields["resume_id"])
	assert.Equal(t, "44", form3.Fields["currency_id"])
	assert.Equal(t, "123", form3.Fields["salary_expectation"])
}
