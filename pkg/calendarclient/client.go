// Package calendarclient dials calendar.v1.CalendarService for product peers (ATS, etc.).
package calendarclient

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"connectrpc.com/connect"
	apis "github.com/antinvestor/common/v2"
	"github.com/antinvestor/common/v2/connection"
	"github.com/antinvestor/common/v2/servicecatalog"

	"github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1/calendarv1connect"
)

// Service identity constants for platform registration.
// Platform common.servicecatalog should add ServiceCalendar = "calendar" with
// AudiencePath "/calendar". Until then, NewClient may fall back to ServiceJobs
// when CALENDAR_OAUTH_SERVICE_ID=jobs (temporary product mesh).
const (
	ServiceID    = "calendar"
	AudiencePath = "/calendar"
	DefaultPort  = "8096"
)

// NewClient dials CalendarService at endpoint via the platform connection stack.
// ServiceID for OAuth audience defaults to "calendar"; if catalog lacks it, set
// CALENDAR_OAUTH_SERVICE_ID=jobs to use ServiceJobs until common is updated.
func NewClient(
	ctx context.Context,
	cfg any,
	endpoint string,
	workloadAPITargetPath string,
) (calendarv1connect.CalendarServiceClient, error) {
	if endpoint == "" {
		return nil, nil
	}
	sid := servicecatalog.ServiceID(strings.TrimSpace(os.Getenv("CALENDAR_OAUTH_SERVICE_ID")))
	if sid == "" {
		// Prefer jobs as temporary known catalog id for product mesh; override with calendar when catalog ships.
		if _, err := servicecatalog.DefinitionFor(servicecatalog.ServiceID(ServiceID)); err == nil {
			sid = servicecatalog.ServiceID(ServiceID)
		} else {
			sid = servicecatalog.ServiceJobs
		}
	}
	cli, err := connection.NewServiceClient(ctx, cfg, apis.ServiceTarget{
		Endpoint:              endpoint,
		WorkloadAPITargetPath: workloadAPITargetPath,
		ServiceID:             sid,
	}, calendarv1connect.NewCalendarServiceClient)
	if err != nil {
		return nil, fmt.Errorf("calendarclient: %w", err)
	}
	return cli, nil
}

// NewDirectClient dials without OAuth mesh (local/dev or gateway that injects auth).
func NewDirectClient(httpClient connect.HTTPClient, baseURL string) calendarv1connect.CalendarServiceClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return calendarv1connect.NewCalendarServiceClient(httpClient, baseURL)
}
