//go:build integration

// service_test.go — integration tests for apps/materializer/service.
//
// The E2E test below spins up real MinIO + Manticore containers and
// is guarded by //go:build integration to keep `go test ./...` fast.
//
// TODO(v6): rewrite TestMaterializerE2E to use an Iceberg-native path
// (MinIO catalog + snapshot-diff scan) rather than the R2-prefix poll
// approach. The old test wires the Phase 5 API (NewService with R2
// reader + Postgres watermarks); that constructor no longer exists.
// Until the E2E test is ported, run unit tests in service_unit_test.go.

package service_test
