package handlers

import (
	"testing"

	"github.com/antinvestor/common/v2/permissions"
	"github.com/stretchr/testify/require"
)

func TestServiceDescriptorAndProcedureMap(t *testing.T) {
	sd := ServiceDescriptor()
	require.NotNil(t, sd)
	require.Equal(t, "AtsService", string(sd.Name()))

	meta := permissions.ForService(sd)
	require.Equal(t, "service_ats", meta.Namespace)
	require.Contains(t, meta.Permissions, "ats_job_manage")
	require.Contains(t, meta.Permissions, "ats_hire")

	procMap := permissions.BuildProcedureMap(sd)
	require.NotEmpty(t, procMap)
	// Sample procedures
	require.Contains(t, procMap["/ats.v1.AtsService/ListJobs"], "ats_job_view")
	require.Contains(t, procMap["/ats.v1.AtsService/CreateJob"], "ats_job_manage")
	require.Contains(t, procMap["/ats.v1.AtsService/HireApplication"], "ats_hire")
	require.Contains(t, procMap["/ats.v1.AtsService/PublishJob"], "ats_publish")
	require.Contains(t, procMap["/ats.v1.AtsService/ScreenSummary"], "ats_ai_use")
}
