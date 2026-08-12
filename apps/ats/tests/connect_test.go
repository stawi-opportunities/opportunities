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

func (s *ConnectSuite) TestConnectListCreateJobAndPublish() {
	t := s.T()
	ctx, deps := s.CreateService(t)

	mux, err := handlers.NewConnectMux(ctx, deps.Svc, handlers.ConnectOptions{
		AllowDevHeaders: true,
		Idempotency:     deps.Idempotency,
	})
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

	dash, err := cli.GetDashboard(t.Context(), connect.NewRequest(&atsv1.GetDashboardRequest{}))
	require.NoError(t, err)
	require.NotNil(t, dash.Msg.GetDashboard())

	create, err := cli.CreateJob(t.Context(), connect.NewRequest(&atsv1.CreateJobRequest{
		Title: "Connect Role", Description: "typed API", Status: models.JobStatusOpen,
	}))
	require.NoError(t, err)
	require.Equal(t, "Connect Role", create.Msg.GetJob().GetTitle())
	require.Equal(t, "t1", create.Msg.GetJob().GetTenantId())

	list, err := cli.ListJobs(t.Context(), connect.NewRequest(&atsv1.ListJobsRequest{}))
	require.NoError(t, err)
	require.NotEmpty(t, list.Msg.GetJobs())

	pubReq := connect.NewRequest(&atsv1.PublishJobRequest{Id: create.Msg.GetJob().GetId()})
	pubReq.Header().Set("Idempotency-Key", "pub-1")
	pub, err := cli.PublishJob(t.Context(), pubReq)
	require.NoError(t, err)
	require.Equal(t, models.VisibilityPublished, pub.Msg.GetJob().GetVisibility())
	require.NotEmpty(t, pub.Msg.GetJob().GetOpportunityId())

	// Second publish with same key should still succeed (domain + interceptor).
	pub2, err := cli.PublishJob(t.Context(), pubReq)
	require.NoError(t, err)
	require.Equal(t, pub.Msg.GetJob().GetOpportunityId(), pub2.Msg.GetJob().GetOpportunityId())
}
