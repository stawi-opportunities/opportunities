package autoapply

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractFormHTML_PicksApplicationForm(t *testing.T) {
	html := `<html>
<form action="/search"><input name="q"/><input type="submit" value="Search"/></form>
<form action="/newsletter"><input name="email"/></form>
<form action="/apply" id="application">
  <input name="first_name" placeholder="First Name"/>
  <input name="last_name" placeholder="Last Name"/>
  <input name="email" placeholder="Email"/>
  <textarea name="cover_letter"></textarea>
  Apply / Resume / Cover Letter
  <input type="submit"/>
</form>
</html>`
	out := extractFormHTML(html, 4000)
	assert.Contains(t, out, "first_name")
	assert.NotContains(t, out, "newsletter")
	assert.NotContains(t, out, "/search")
}

func TestExtractFormHTML_NoFormReturnsEmpty(t *testing.T) {
	assert.Empty(t, extractFormHTML("<html>nothing here</html>", 4000))
}

func TestExtractFormHTML_StripsScriptStyle(t *testing.T) {
	html := `<form action="/apply">
  <script>alert('x')</script>
  <style>.x{color:red}</style>
  <input name="email"/>
</form>`
	out := extractFormHTML(html, 4000)
	assert.NotContains(t, out, "alert")
	assert.NotContains(t, out, "color:red")
	assert.Contains(t, out, "email")
}

func TestExtractFormHTML_TruncatesByRunes(t *testing.T) {
	body := strings.Repeat("a", 5000)
	html := "<form>" + body + "</form>"
	out := extractFormHTML(html, 100)
	assert.LessOrEqual(t, len([]rune(out)), 100)
}

func TestParseFieldMap_StripsCodeFences(t *testing.T) {
	in := "```json\n{\"#email\": \"a@b\", \"#name\": \"Ada\"}\n```"
	m, err := parseFieldMap(in)
	require.NoError(t, err)
	assert.Equal(t, "a@b", m["#email"])
	assert.Equal(t, "Ada", m["#name"])
}

func TestParseFieldMap_HandlesNonStringValues(t *testing.T) {
	in := `{"#age": 30, "#consent": true, "#nick": null}`
	m, err := parseFieldMap(in)
	require.NoError(t, err)
	assert.Equal(t, "30", m["#age"])
	assert.Equal(t, "true", m["#consent"])
}

func TestParseFieldMap_BadJSONErrors(t *testing.T) {
	_, err := parseFieldMap("not json at all")
	require.Error(t, err)
}

func TestStripTag_RepeatsAndOverlaps(t *testing.T) {
	in := `before<script>a</script>middle<script>b</script>after`
	out := stripTag(in, "script")
	assert.Equal(t, "beforemiddleafter", out)
}

func TestStripTag_UnmatchedDropsRest(t *testing.T) {
	in := `before<script>a</script>middle<script>oops`
	out := stripTag(in, "script")
	assert.Equal(t, "beforemiddle", out)
}
