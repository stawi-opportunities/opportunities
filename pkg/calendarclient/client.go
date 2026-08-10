// Package calendarclient dials calendar.v1.CalendarService for product peers (ATS, etc.).
package calendarclient

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	apis "github.com/antinvestor/common/v2"
	"github.com/antinvestor/common/v2/connection"

	"github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1/calendarv1connect"
)

// ServiceID is the OAuth audience path segment for calendar (platform catalog).
// Until common servicecatalog ships ServiceCalendar, products pass this path
// explicitly via ServiceTarget when dialling.
const (
	ServiceID    = "calendar"
	AudiencePath = "/calendar"
	DefaultPort  = "8096"
)

// NewClient dials CalendarService at endpoint (e.g. https://api…/calendar).
// cfg is the Frame/service config used by connection.NewServiceClient.
// When workloadAPITargetPath is empty, default mesh path may be used by connection.
func NewClient(
	ctx context.Context,
	cfg any,
	endpoint string,
	workloadAPITargetPath string,
) (calendarv1connect.CalendarServiceClient, error) {
	if endpoint == "" {
		return nil, nil
	}
	return connection.NewServiceClient(ctx, cfg, apis.ServiceTarget{
		Endpoint:              endpoint,
		WorkloadAPITargetPath: workloadAPITargetPath,
		ServiceID:             ServiceID,
	}, calendarv1connect.NewCalendarServiceClient)
}

// NewDirectClient dials without OAuth mesh (local/dev HTTP).
func NewDirectClient(httpClient connect.HTTPClient, baseURL string) calendarv1connect.CalendarServiceClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return calendarv1connect.NewCalendarServiceClient(httpClient, baseURL)
}
