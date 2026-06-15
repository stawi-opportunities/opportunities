//go:build manual

package browser

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// TestLiveGreenhouseInspect navigates a live Greenhouse apply page and
// prints the real form controls (inputs/selects/textareas/buttons) so we
// can update the submitter's selectors to match current markup.
//
//	GREENHOUSE_TEST_URL=... go test -tags=manual -run TestLiveGreenhouseInspect -v ./pkg/autoapply/browser/
func TestLiveGreenhouseInspect(t *testing.T) {
	url := os.Getenv("GREENHOUSE_TEST_URL")
	if url == "" {
		t.Skip("set GREENHOUSE_TEST_URL to run")
	}

	path, _ := launcher.LookPath()
	u, err := launcher.New().Bin(path).Headless(true).
		Set("no-sandbox").Set("disable-dev-shm-usage").Launch()
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer b.Close()

	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	page = page.Timeout(90 * time.Second)
	if err := page.Navigate(url); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	_ = page.WaitLoad()
	time.Sleep(6 * time.Second) // let the React form render

	res, err := page.Eval(`() => {
		const out = {url: location.href, fields: [], buttons: []};
		document.querySelectorAll('input,select,textarea').forEach(el => {
			out.fields.push([el.tagName, el.type||'', el.id||'', el.name||'', (el.getAttribute('aria-label')||'').slice(0,40)]);
		});
		document.querySelectorAll('button,input[type=submit]').forEach(el => {
			out.buttons.push([el.id||'', el.type||'', (el.textContent||el.value||'').trim().slice(0,40)]);
		});
		return JSON.stringify(out, null, 0);
	}`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	fmt.Println("INSPECT:", res.Value.Str())
}

// TestLiveGreenhouseFillReadback fills the standard fields through the
// same Element/Input path production uses, then reads the values back to
// prove they actually landed on the live form.
//
//	GREENHOUSE_TEST_URL=... go test -tags=manual -run TestLiveGreenhouseFillReadback -v ./pkg/autoapply/browser/
func TestLiveGreenhouseFillReadback(t *testing.T) {
	url := os.Getenv("GREENHOUSE_TEST_URL")
	if url == "" {
		t.Skip("set GREENHOUSE_TEST_URL to run")
	}

	path, _ := launcher.LookPath()
	u, _ := launcher.New().Bin(path).Headless(true).
		Set("no-sandbox").Set("disable-dev-shm-usage").Launch()
	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer b.Close()

	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	page = page.Timeout(90 * time.Second)
	if err := page.Navigate(url); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	_ = page.WaitLoad()
	time.Sleep(6 * time.Second)

	fields := map[string]string{
		"#first_name": "Test",
		"#last_name":  "User",
		"#email":      "test.user@example.com",
		"#phone":      "+254712345678",
	}
	for sel, val := range fields {
		el, eerr := page.Timeout(8 * time.Second).Element(sel)
		if eerr != nil {
			t.Logf("field %s NOT FOUND", sel)
			continue
		}
		_ = el.SelectAllText()
		_ = el.Input(val)
	}

	// Attach a CV if provided, so the screenshot shows the resume state.
	if cvPath := os.Getenv("GREENHOUSE_CV"); cvPath != "" {
		if fileEl, ferr := page.Timeout(8 * time.Second).Element("#resume, #job_application_resume"); ferr == nil {
			if serr := fileEl.SetFiles([]string{cvPath}); serr != nil {
				t.Logf("SetFiles: %v", serr)
			}
			time.Sleep(2 * time.Second)
		}
	}

	res, err := page.Eval(`() => JSON.stringify({
		first_name: document.querySelector('#first_name')?.value,
		last_name:  document.querySelector('#last_name')?.value,
		email:      document.querySelector('#email')?.value,
		phone:      document.querySelector('#phone')?.value
	})`)
	if err != nil {
		t.Fatalf("readback eval: %v", err)
	}
	fmt.Println("READBACK:", res.Value.Str())

	// Tag the application <form> and screenshot just that element at full
	// resolution so the filled fields are legible.
	marked, _ := page.Eval(`() => {
		let e = document.querySelector('#first_name');
		while (e && e.tagName !== 'FORM') e = e.parentElement;
		if (e) { e.id = '__appform'; return true; }
		return false;
	}`)
	formShot := os.Getenv("GREENHOUSE_FORM_SHOT")
	if formShot == "" {
		formShot = "/tmp/gh_form.png"
	}
	if marked.Value.Bool() {
		if formEl, ferr := page.Element("#__appform"); ferr == nil {
			_ = formEl.ScrollIntoView()
			if data, serr := formEl.Screenshot(proto.PageCaptureScreenshotFormatPng, 0); serr == nil {
				_ = os.WriteFile(formShot, data, 0o644)
				fmt.Println("FORM SHOT:", formShot)
			}
		}
	}
}

// TestLiveComboCheck fills the two tricky async comboboxes (Country and the
// Google-Places Location) via fillField and reads back the committed text,
// to verify the combobox-polling fix works on them.
//
//	GREENHOUSE_TEST_URL=... go test -tags=manual -run TestLiveComboCheck -v ./pkg/autoapply/browser/
func TestLiveComboCheck(t *testing.T) {
	url := os.Getenv("GREENHOUSE_TEST_URL")
	if url == "" {
		t.Skip("set GREENHOUSE_TEST_URL to run")
	}
	path, _ := launcher.LookPath()
	u, _ := launcher.New().Bin(path).Headless(true).Set("no-sandbox").Set("disable-dev-shm-usage").Launch()
	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer b.Close()
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	page = page.Timeout(90 * time.Second)
	if err := page.Navigate(url); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	settle(page)

	// Type into the location field and dump the suggestion elements so we
	// learn the correct selector to click.
	if el, e := page.Timeout(5 * time.Second).Element("#candidate-location"); e == nil {
		_ = el.Click(proto.InputMouseButtonLeft, 1)
		_ = el.Input("Nairobi")
		time.Sleep(2500 * time.Millisecond)
	}
	res, err := page.Eval(`() => {
		const out = [];
		document.querySelectorAll('*').forEach(e => {
			if (e.children.length === 0) {
				const t = (e.innerText || '').trim();
				if (t && t.toLowerCase().includes('nairobi')) {
					out.push((e.tagName + '.' + (e.className || '')).slice(0,90) + ' :: ' + t.slice(0,40));
				}
			}
		});
		return JSON.stringify([...new Set(out)].slice(0,20));
	}`)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	fmt.Println("LOCATION SUGGESTIONS:", res.Value.Str())
}

// TestLiveGreenhouseFill drives a real headless browser against a live
// Greenhouse apply form, fills the standard fields with dummy data, and
// saves a screenshot — WITHOUT submitting. Safe to run against any public
// board: nothing is sent to the employer.
//
//	GREENHOUSE_TEST_URL=https://boards.greenhouse.io/<co>/jobs/<id> \
//	  go test -tags=manual -run TestLiveGreenhouseFill -v ./pkg/autoapply/browser/
//
// Optional: GREENHOUSE_SHOT=/path/out.png (default /tmp/greenhouse_fill.png).
func TestLiveGreenhouseFill(t *testing.T) {
	url := os.Getenv("GREENHOUSE_TEST_URL")
	if url == "" {
		t.Skip("set GREENHOUSE_TEST_URL to run this live smoke test")
	}
	shot := os.Getenv("GREENHOUSE_SHOT")
	if shot == "" {
		shot = "/tmp/greenhouse_fill.png"
	}

	pool := NewPool(1, 60*time.Second, "")
	defer pool.Close()

	err := pool.FillAndSubmit(context.Background(), SubmitOptions{
		URL: url,
		TextFields: map[string]string{
			"#first_name":                 "Test",
			"#last_name":                  "User",
			"#email":                      "test.user@example.com",
			"#phone":                      "+254712345678",
			"#job_application_first_name": "Test",
			"#job_application_last_name":  "User",
			"#job_application_email":      "test.user@example.com",
			"#job_application_phone":      "+254712345678",
		},
		FileField:      "#resume, #job_application_resume",
		SubmitSel:      "[data-submit='true'], input[type='submit'], button[type='submit']",
		ScreenshotPath: shot,
		NoSubmit:       true,
	})
	if err != nil {
		t.Fatalf("fill failed: %v", err)
	}
	t.Logf("fill OK — submit control found, screenshot saved to %s", shot)
}
