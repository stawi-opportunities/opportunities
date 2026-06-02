# Lean Postgres Plan 3: Candidate Profile Slimming

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drop dead schema (`cv_raw_text` — column has zero writers in production code), promote skill arrays from comma-separated TEXT to `text[]` with a GIN index for fast `@>` queries, and pre-create the R2-pointer columns (`cv_storage_uri`, `cv_content_hash`) so the next iteration can wire the CV-upload path through R2 without another schema change.

**Architecture:** Audit findings (2026-05-28):
- `CVRawText` is defined on `domain.CandidateProfile` (line 49) and the SQL column exists, but no production code path writes it (`grep -rn "CVRawText\s*[:=]" --include="*.go"` returned zero hits outside tests). Pure dead schema.
- Skill columns (`skills`, `strong_skills`, `working_skills`, `tools_frameworks`) are stored as comma-separated TEXT, then split at read time via `strings.Split(s, ",")` (`pkg/candidatestore/profile_fields.go:112,135`). Unindexable for "find candidates with skill X" queries.
- `candidate_profiles` has 0 rows in production. Safe to do a clean type swap with `ALTER COLUMN ... USING string_to_array(...)`.
- `Bio` and `WorkHistory` ARE read (cv_embed.go reads Bio for embedding text; cv/fixes.go + cv/scorer.go read WorkHistory for CV scoring). They stay in Postgres.

This plan leaves the actual R2 CV-upload wiring for a follow-up: the columns will be present and the type system primed, but no code change to the upload flow is in this plan because there is no current upload-to-table-write flow to redirect.

**Tech Stack:** Go, GORM, Postgres 16 (with `pq.Array` for text[] handling — already used at `pkg/matching/index_store.go:67`).

**Depends on:** Independent of Plans 1 and 2. Can ship in any order.

---

## File Structure

**Created:**
- `db/migrations/0022_candidate_lean.sql` — drop `cv_raw_text`, add `cv_storage_uri` + `cv_content_hash`, swap four skill columns to `text[]`, add GIN index.

**Modified:**
- `pkg/domain/candidate.go` lines 47-60 — remove `CVRawText`, add `CVStorageURI` + `CVContentHash`, change four `Skills` fields from `string` to `[]string` (or `pq.StringArray` for GORM compatibility).
- `pkg/candidatestore/profile_fields.go` — remove `splitCSV` calls on the four skill fields, read directly as arrays.

**Touched:**
- `apps/writer/service/arrow_build.go` if it references `cv_raw_text` or the skill columns in any of the Iceberg row encoders (`grep -n "cv_raw_text\|strong_skills" apps/writer/service/arrow_build.go`).

---

## Tasks

### Task 1: Migration — schema swap

**Files:**
- Create: `db/migrations/0022_candidate_lean.sql`

- [ ] **Step 1: Write the migration**

```sql
-- 0022: lean up candidate_profiles.
--
-- Per docs/superpowers/specs/2026-05-28-lean-postgres-design.md:
--   1. cv_raw_text: zero production writers (verified by grep);
--      remove the column entirely.
--   2. cv_storage_uri + cv_content_hash: future R2 path pointer.
--      Added now so the next code iteration can wire CV uploads
--      through R2 without another migration.
--   3. Skills columns: comma-separated TEXT → text[] for GIN-indexed
--      `@>` queries ("candidates whose strong_skills contain
--      'python'"). At 100 k candidates a CSV split-on-read is
--      ~10x slower than an array containment check on a GIN index.
--
-- Safe to do a destructive type swap because candidate_profiles has
-- 0 rows in production (verified 2026-05-28). USING string_to_array
-- preserves any rows that DO show up between merge and deploy.

BEGIN;

-- ---------- drop dead column ----------

ALTER TABLE candidate_profiles
    DROP COLUMN IF EXISTS cv_raw_text;

-- ---------- add R2 pointer columns ----------

ALTER TABLE candidate_profiles
    ADD COLUMN IF NOT EXISTS cv_storage_uri  TEXT,
    ADD COLUMN IF NOT EXISTS cv_content_hash VARCHAR(64);

-- ---------- skills columns: TEXT (CSV) → text[] ----------

-- USING string_to_array with NULLIF so a NULL or empty-string input
-- becomes an empty array (NOT a one-element array containing "").
ALTER TABLE candidate_profiles
    ALTER COLUMN skills           TYPE text[]
        USING CASE WHEN COALESCE(skills,'') = ''           THEN ARRAY[]::text[]
                   ELSE string_to_array(skills, ',') END,
    ALTER COLUMN strong_skills    TYPE text[]
        USING CASE WHEN COALESCE(strong_skills,'') = ''    THEN ARRAY[]::text[]
                   ELSE string_to_array(strong_skills, ',') END,
    ALTER COLUMN working_skills   TYPE text[]
        USING CASE WHEN COALESCE(working_skills,'') = ''   THEN ARRAY[]::text[]
                   ELSE string_to_array(working_skills, ',') END,
    ALTER COLUMN tools_frameworks TYPE text[]
        USING CASE WHEN COALESCE(tools_frameworks,'') = '' THEN ARRAY[]::text[]
                   ELSE string_to_array(tools_frameworks, ',') END;

-- GIN indexes for `@>` containment queries. Strong/working skills are
-- the matcher hot path; the other two are not yet — add their GINs
-- when the query pattern emerges.
CREATE INDEX IF NOT EXISTS candidate_profiles_strong_skills_gin_idx
    ON candidate_profiles USING gin (strong_skills);

CREATE INDEX IF NOT EXISTS candidate_profiles_working_skills_gin_idx
    ON candidate_profiles USING gin (working_skills);

COMMIT;
```

- [ ] **Step 2: Verify migration applies cleanly**

```bash
cd /home/j/code/stawi.opportunities && go test ./tests/integration/... -run TestMigrationsApplyCleanly -count=1 -v 2>&1 | tail -10
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add db/migrations/0022_candidate_lean.sql
git commit -m "feat(lean): drop cv_raw_text, promote skills to text[]

cv_raw_text had zero production writers (verified by grep). Removing
it shrinks the row and removes future bloat risk.

Adds cv_storage_uri + cv_content_hash columns so a follow-up
iteration can wire CV uploads through R2 without another migration.

Promotes the four skill columns from comma-separated TEXT to text[]
with GIN indexes on strong_skills + working_skills — enables
indexed @> containment queries for the matcher hot path."
```

---

### Task 2: Update domain.CandidateProfile struct

**Files:**
- Modify: `pkg/domain/candidate.go` lines 47-60.

- [ ] **Step 1: Write the failing test**

Append to `pkg/domain/candidate_test.go` (create the file if it doesn't exist):

```go
package domain_test

import (
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func TestCandidateProfile_SkillsAreSlices(t *testing.T) {
	p := domain.CandidateProfile{
		StrongSkills:  []string{"python", "postgres"},
		WorkingSkills: []string{"go", "kubernetes"},
	}
	if len(p.StrongSkills) != 2 {
		t.Fatalf("StrongSkills len = %d; want 2", len(p.StrongSkills))
	}
}

func TestCandidateProfile_HasR2PointerFields(t *testing.T) {
	p := domain.CandidateProfile{
		CVStorageURI:  "candidates/abc/cv-raw.txt.gz",
		CVContentHash: "deadbeef",
	}
	if p.CVStorageURI == "" {
		t.Fatal("CVStorageURI not set")
	}
	if p.CVContentHash == "" {
		t.Fatal("CVContentHash not set")
	}
}

func TestCandidateProfile_NoCVRawText(t *testing.T) {
	// Compile-time check: domain.CandidateProfile must not have a CVRawText field.
	// If this test fails to compile, the field still exists.
	var _ = domain.CandidateProfile{} // sanity: type still compiles
	// Use reflection to assert the field is gone.
	var p domain.CandidateProfile
	v := reflect.ValueOf(p)
	if v.FieldByName("CVRawText").IsValid() {
		t.Fatal("CVRawText field still exists on CandidateProfile; expected removed")
	}
}
```

Add `import "reflect"` at the top of the test file.

- [ ] **Step 2: Run, verify the third test fails (CVRawText still defined)**

```bash
cd /home/j/code/stawi.opportunities && go test ./pkg/domain/ -run TestCandidateProfile -v 2>&1 | tail -10
```
Expected: FAIL on `TestCandidateProfile_NoCVRawText` (CVRawText exists) and `TestCandidateProfile_SkillsAreSlices` (type mismatch — `Skills` is currently string).

- [ ] **Step 3: Edit the struct**

In `pkg/domain/candidate.go` around lines 47-60:

```go
// CV storage — raw text lives in R2; the row carries only the pointer.
CVUrl         string `gorm:"type:text" json:"cv_url"`
CVStorageURI  string `gorm:"type:text" json:"-"`
CVContentHash string `gorm:"type:varchar(64)" json:"-"`

// AI-extracted profile fields
CurrentTitle    string         `gorm:"type:text" json:"current_title"`
Seniority       string         `gorm:"type:varchar(30)" json:"seniority"`
YearsExperience int            `gorm:"type:int" json:"years_experience"`
Skills          pq.StringArray `gorm:"type:text[]" json:"skills"`
StrongSkills    pq.StringArray `gorm:"type:text[]" json:"strong_skills"`
WorkingSkills   pq.StringArray `gorm:"type:text[]" json:"working_skills"`
ToolsFrameworks pq.StringArray `gorm:"type:text[]" json:"tools_frameworks"`
Certifications  string         `gorm:"type:text" json:"certifications"`
PreferredRoles  string         `gorm:"type:text" json:"preferred_roles"`
Industries      string         `gorm:"type:text" json:"industries"`
Education       string         `gorm:"type:text" json:"education"`
```

Remove the old `CVRawText` line entirely. Add `"github.com/lib/pq"` to the imports at the top of the file (the codebase already uses `pq.Array` elsewhere — `pkg/matching/index_store.go:67`).

- [ ] **Step 4: Run, verify pass**

```bash
go test ./pkg/domain/ -run TestCandidateProfile -v 2>&1 | tail -10
go build ./... 2>&1 | tail -10
```
Expected: PASS. The build may surface every caller that read `.Skills` as a string — fix each by treating it as `[]string` (the API + matcher already model it as `[]string` per `pkg/candidatestore/profile_fields.go:30`, so the fix is to remove the `splitCSV` wrapper, not propagate the string).

- [ ] **Step 5: Commit**

```bash
git add pkg/domain/candidate.go pkg/domain/candidate_test.go
git commit -m "feat(lean): CandidateProfile.Skills as text[]; drop CVRawText

Removes the dead CVRawText field, adds CVStorageURI + CVContentHash
for the future R2 path, and switches the four skill fields to
pq.StringArray so GORM round-trips them as text[] (matching
migration 0022). Callers that read .Skills as comma-separated
string need to stop splitCSV — track those in the next task."
```

---

### Task 3: Update candidatestore reads to stop splitCSV on skills

**Files:**
- Modify: `pkg/candidatestore/profile_fields.go` lines 90-130 (the SELECT projection + the assignment to `pf.Skills` / `pf.StrongSkills` / etc.).

- [ ] **Step 1: Inspect current code**

```bash
sed -n '60,140p' pkg/candidatestore/profile_fields.go
```

Look for the SELECT statement that pulls `skills`, `strong_skills`, `working_skills`, `tools_frameworks` columns, and the subsequent `splitCSV(strongRaw)` calls that populate the struct.

- [ ] **Step 2: Update the SELECT + binding**

The SQL no longer needs to project the columns as TEXT — they're already `text[]`. Use `pq.Array(&pf.StrongSkills)` to bind into the slice directly. Pattern is identical to the existing usage at `pkg/matching/index_store.go:67`.

```go
// Old (illustrative — exact lines vary):
var strongRaw, workingRaw string
err := r.db.QueryRowContext(ctx, `
    SELECT
        COALESCE(skills,''),
        COALESCE(strong_skills,''),
        COALESCE(working_skills,''),
        ...
    FROM candidate_profiles WHERE id = $1
`, id).Scan(&strongRaw, ..., &workingRaw, ..., &pf.Bio, ...)
pf.StrongSkills = splitCSV(strongRaw)
pf.WorkingSkills = splitCSV(workingRaw)

// New:
err := r.db.QueryRowContext(ctx, `
    SELECT
        COALESCE(skills, ARRAY[]::text[]),
        COALESCE(strong_skills, ARRAY[]::text[]),
        COALESCE(working_skills, ARRAY[]::text[]),
        COALESCE(tools_frameworks, ARRAY[]::text[]),
        ...
    FROM candidate_profiles WHERE id = $1
`, id).Scan(
    pq.Array(&pf.Skills),
    pq.Array(&pf.StrongSkills),
    pq.Array(&pf.WorkingSkills),
    pq.Array(&pf.ToolsFrameworks),
    ...,
)
```

Remove the helper `splitCSV` if it's no longer referenced anywhere else (`grep -n "splitCSV" pkg/`).

- [ ] **Step 3: Update the matching tests test scaffold**

The test `pkg/candidatestore/profile_fields_test.go:32` declares an in-memory schema with `strong_skills TEXT`. Update to `strong_skills text[]` to match production.

- [ ] **Step 4: Run tests**

```bash
go test ./pkg/candidatestore/... -count=1 -v 2>&1 | tail -15
go test ./pkg/matching/... -count=1 2>&1 | tail -10
go build ./... 2>&1 | tail -5
```
Expected: PASS. If any matching test fails because it constructed a CandidateProfile with a comma-separated string and now expects []string, fix the test to use the slice form.

- [ ] **Step 5: Commit**

```bash
git add pkg/candidatestore/ pkg/matching/
git commit -m "feat(lean): candidatestore reads text[] skill columns directly

Removes splitCSV at read time. pq.Array binds the Postgres text[]
columns straight into pq.StringArray on the domain struct."
```

---

### Task 4: Check + update writer Arrow schema

**Files:**
- Modify (if needed): `apps/writer/service/arrow_build.go`.

The writer encodes candidate-related events for Iceberg. If it references `cv_raw_text` or the skill columns by the old TEXT type, those encoders need updating.

- [ ] **Step 1: Inspect**

```bash
grep -n "cv_raw_text\|strong_skills\|working_skills\|tools_frameworks" apps/writer/service/arrow_build.go apps/writer/service/arrow_schemas.go 2>&1 | head -20
```

- [ ] **Step 2: Update or no-op**

If grep returns hits referencing the old TEXT type, switch them to the list-of-strings Arrow type. If the writer doesn't encode these columns at all (they may flow through events as JSON, not through the writer's structured schemas), nothing to do here.

- [ ] **Step 3: Build + test**

```bash
go test ./apps/writer/... -count=1 2>&1 | tail -10
go build ./... 2>&1 | tail -5
```

- [ ] **Step 4: Commit if changes were needed**

```bash
git add apps/writer/service/arrow_build.go apps/writer/service/arrow_schemas.go
git commit -m "fix(writer): adapt Arrow encoders to text[] skill columns"
```

(Skip this step if no writer changes were needed.)

---

### Task 5: Tag + deploy v8.0.61

- [ ] **Step 1: Push, tag**

```bash
cd /home/j/code/stawi.opportunities
git push origin main
git tag v8.0.61
git push origin v8.0.61
```

- [ ] **Step 2: Wait for build + flux**

```bash
gh run watch $(gh run list --limit 1 --workflow release.yaml --repo stawi-opportunities/opportunities --json databaseId -q '.[0].databaseId') --repo stawi-opportunities/opportunities --exit-status
```

- [ ] **Step 3: Verify in cluster**

```bash
# Schema check
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "\d candidate_profiles" | grep -E "cv_storage_uri|cv_content_hash|cv_raw_text|skills"

# GIN indexes
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "
SELECT indexname FROM pg_indexes
WHERE tablename = 'candidate_profiles'
  AND indexname LIKE '%skills_gin%'"

# Quick query check: type system
kubectl exec -n product-opportunities product-opportunities-db-1 -c postgres -- psql -U postgres -d opportunities -c "
SELECT column_name, data_type
FROM information_schema.columns
WHERE table_name = 'candidate_profiles'
  AND column_name IN ('skills','strong_skills','working_skills','tools_frameworks','cv_raw_text','cv_storage_uri','cv_content_hash')
ORDER BY column_name"
```

Expected:
- `cv_raw_text` is gone.
- `cv_storage_uri` + `cv_content_hash` present.
- Four skill columns at `data_type = 'ARRAY'`.
- Two `*_skills_gin_idx` indexes present.

---

## Plan 3 Exit Criteria

- [ ] `cv_raw_text` column removed.
- [ ] `cv_storage_uri` + `cv_content_hash` columns present (currently NULL on all rows; will be populated by the next iteration when CV upload is rewired through R2).
- [ ] `skills`, `strong_skills`, `working_skills`, `tools_frameworks` are all `text[]`.
- [ ] GIN indexes on `strong_skills` and `working_skills`.
- [ ] No regressions in candidatestore / matching tests.
- [ ] `splitCSV` helper deleted (or scoped to just the remaining TEXT-CSV columns that didn't move in this plan).

---

## Follow-Up (Not in This Plan)

- **Wire CV uploads through R2.** The columns are ready; the upload entry point (in the applications or onboarding service) needs to PUT to `candidates/{candidate_id}/cv-raw.txt.gz` first, compute sha256, then set the two columns. The original CV-extraction flow already runs (the extracted structured fields are what matters today); this just adds the durable raw copy back for future re-extraction. ~1 hour of work; deserves its own small spec + plan once the upload path is identified.
- **Promote remaining skill-like CSV columns.** `preferred_locations`, `preferred_countries`, `preferred_regions`, `preferred_timezones`, `languages`, `industries`, `certifications`, `preferred_roles`, `education` are all comma-separated TEXT today. Promote them to `text[]` when their query patterns demand it (no need to do it preemptively).
