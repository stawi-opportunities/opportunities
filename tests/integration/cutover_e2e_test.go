//go:build integration
// +build integration

package integration

import (
	"testing"
)

// TestCutoverE2E is the full pipeline smoke: seed a source, fire
// scheduler-tick, watch crawler → worker → writer → materializer,
// then assert /api/v2/search returns the seeded job. Requires a live
// k8s cluster (or composed testcontainers stack) running all six
// services. Implement the harness in tests/integration/helpers_test.go
// when cluster access is ready.
func TestCutoverE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("cutover e2e skipped under -short")
	}
	t.Skip("cutover e2e requires live cluster access — see docs/ops/cutover-runbook.md §1 staging smoke")
}
