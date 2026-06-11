package repository

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// servingDDL embeds the raw-SQL OLTP serving migrations verbatim.
//
// The embed directive cannot reach the canonical db/migrations directory (it
// lives two levels up from this package, and embed patterns may not traverse
// parent directories), so the four serving-table migrations are copied
// byte-for-byte into serving_ddl/ and embedded from there. Keeping the *.sql
// files embedded
// (rather than hand-copying the DDL into Go string literals) means the SQL is
// a single source of truth that can't silently drift from real Postgres DDL.
//
//go:embed serving_ddl/0010_applications_oltp.sql
//go:embed serving_ddl/0014_applications_phase3.sql
//go:embed serving_ddl/0018_candidate_saved_jobs.sql
//go:embed serving_ddl/0020_candidate_checkouts.sql
var servingDDL embed.FS

// servingDDLFiles is the ordered list of embedded serving migrations.
// Order matters: application_notes/attachments/reminders (0010) and the
// phase-3 tables (0014) have FK references to applications(application_id),
// so 0010 must run before 0014.
var servingDDLFiles = []string{
	"serving_ddl/0010_applications_oltp.sql",
	"serving_ddl/0014_applications_phase3.sql",
	"serving_ddl/0018_candidate_saved_jobs.sql",
	"serving_ddl/0020_candidate_checkouts.sql",
}

// EnsureServingTables creates the raw-SQL OLTP serving tables that the
// matching service's serving code reads/writes but that GORM AutoMigrate does
// not own.
//
// These tables (applications, application_notes, application_attachments,
// application_reminders, idempotency_keys, candidate_saved_jobs,
// candidate_checkouts) were historically applied to production by hand from
// db/migrations/{0010,0014,0018,0020}.sql. On a fresh database the AutoMigrate
// migrate path would never create them, breaking the tracking feed, apply,
// save, and billing flows.
//
// Note the table-name schism: the GORM domain.CandidateApplication maps to
// table candidate_applications, but the serving code uses the raw-SQL
// applications table. Adding a GORM model here would create the WRONG table,
// so the DDL is applied verbatim from the embedded migrations instead.
//
// Every statement is CREATE TABLE/INDEX IF NOT EXISTS plain Postgres (no
// TimescaleDB hypertables or continuous aggregates), so this is idempotent and
// safe to run on every migrate. Call it once immediately after FinalizeSchema.
func EnsureServingTables(ctx context.Context, db *gorm.DB) error {
	for _, name := range servingDDLFiles {
		sql, err := servingDDL.ReadFile(name)
		if err != nil {
			return fmt.Errorf("ensure serving tables: read %q: %w", name, err)
		}
		// Each Exec runs as a prepared statement, which cannot carry multiple
		// commands ("cannot insert multiple commands into a prepared statement",
		// 42601). These DDL files hold several CREATE statements each, so split
		// and run them one at a time. (Without this, EnsureServingTables fails on
		// every migrate regardless of DB state — it kept the matching deploy
		// stuck for 26 versions.)
		for _, stmt := range splitSQLStatements(string(sql)) {
			if execErr := db.WithContext(ctx).Exec(stmt).Error; execErr != nil {
				return fmt.Errorf("ensure serving tables: exec %q: %w", name, execErr)
			}
		}
	}
	return nil
}

// splitSQLStatements splits a plain DDL document into individual statements on
// semicolon boundaries, dropping full-line "--" comments and blank lines. It is
// intentionally line-oriented: the serving_ddl files contain only CREATE
// TABLE/INDEX statements — no dollar-quoted bodies, DO blocks, functions, or
// string-literal semicolons — so a statement always ends at a line whose
// trimmed text ends in ";". Needed because each Exec is one prepared statement
// and cannot carry multiple commands.
func splitSQLStatements(s string) []string {
	var out []string
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		if strings.HasSuffix(trimmed, ";") {
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
		}
	}
	if rem := strings.TrimSpace(b.String()); rem != "" {
		out = append(out, rem)
	}
	return out
}
