package repository

import (
	"strings"
	"testing"
)

// TestSplitSQLStatements_EmbeddedDDLAreSingleCommands verifies that every
// embedded serving-DDL file splits into individual single-command statements —
// the property EnsureServingTables needs, since each Exec is one prepared
// statement and cannot carry multiple commands (SQLSTATE 42601).
func TestSplitSQLStatements_EmbeddedDDLAreSingleCommands(t *testing.T) {
	for _, name := range servingDDLFiles {
		raw, err := servingDDL.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		stmts := splitSQLStatements(string(raw))
		if len(stmts) == 0 {
			t.Fatalf("%s: parsed 0 statements", name)
		}
		for _, s := range stmts {
			if c := strings.Count(s, ";"); c != 1 {
				t.Errorf("%s: not a single command (%d semicolons):\n%s", name, c, s)
			}
			if !strings.HasSuffix(strings.TrimSpace(s), ";") {
				t.Errorf("%s: statement does not end in ';':\n%s", name, s)
			}
		}
	}
}

func TestSplitSQLStatements_DropsCommentsAndBlanks(t *testing.T) {
	in := "-- a comment\n\nCREATE TABLE x (id TEXT);\n-- another\nCREATE INDEX i\n  ON x (id);\n"
	got := splitSQLStatements(in)
	if len(got) != 2 {
		t.Fatalf("want 2 statements, got %d: %#v", len(got), got)
	}
	if !strings.HasPrefix(got[0], "CREATE TABLE x") || !strings.Contains(got[1], "CREATE INDEX i") {
		t.Fatalf("unexpected split: %#v", got)
	}
}
