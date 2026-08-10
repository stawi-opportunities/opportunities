package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	atsv1 "github.com/stawi-opportunities/opportunities/apps/ats/gen/ats/v1"
	"github.com/stawi-opportunities/opportunities/apps/ats/gen/ats/v1/atsv1connect"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/handlers"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
)

type ConnectSuite struct {
	ATSBaseTestSuite
}

func TestConnectSuite(t *testing.T) {
	suite.Run(t, new(ConnectSuite))
}

func (s *ConnectSuite) TestConnectListCreateJobAndSeed() {
	t := s.T()
	ctx, deps := s.CreateService(t)

	mux, err := handlers.NewConnectMux(ctx, deps.Svc, nil, true)
	require.NoError(t, err)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cli := atsv1connect.NewAtsServiceClient(http.DefaultClient, srv.URL, connect.WithInterceptors(
		connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				req.Header().Set("X-Profile-ID", "rec-1")
				req.Header().Set("X-Tenant-ID", "t1")
				req.Header().Set("X-Partition-ID", "p1")
				return next(ctx, req)
			}
		}),
	))

	_ = ctx
	seedResp, err := cli.SeedDemo(t.Context(), connect.NewRequest(&atsv1.SeedDemoRequest{}))
	require.NoError(t, err)
	require.True(t, seedResp.Msg.GetSeeded() || seedResp.Msg.GetDashboard() != nil)

	list, err := cli.ListJobs(t.Context(), connect.NewRequest(&atsv1.ListJobsRequest{}))
	require.NoError(t, err)
	require.NotEmpty(t, list.Msg.GetJobs())

	create, err := cli.CreateJob(t.Context(), connect.NewRequest(&atsv1.CreateJobRequest{
		Title: "Connect Role", Description: "typed API", Status: models.JobStatusOpen,
	}))
	require.NoError(t, err)
	require.Equal(t, "Connect Role", create.Msg.GetJob().GetTitle())
	require.Equal(t, "t1", create.Msg.GetJob().GetTenantId())
}
