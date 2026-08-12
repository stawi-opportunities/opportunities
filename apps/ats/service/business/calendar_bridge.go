package business

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
	calendarv1 "github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1"
	"github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1/calendarv1connect"
)

// InterviewCalendar is the required reservation plane (service_calendar).
// ATS does not compute slots or free/busy locally.
type InterviewCalendar interface {
	EnsurePanelResources(ctx context.Context, profileIDs []string) (resourceIDs []string, err error)
	SyncProfileAvailability(ctx context.Context, profileID, timezone string, rules []models.WeekRule, exceptions []models.ExceptionDay) error
	GetProfileAvailability(ctx context.Context, profileID string) (timezone string, rules []models.WeekRule, exceptions []models.ExceptionDay, err error)
	ListPanelSlots(ctx context.Context, resourceIDs []string, durationMin int, windowStart, windowEnd time.Time) ([]models.Slot, error)
	BookPanel(ctx context.Context, resourceIDs []string, start, end time.Time, interviewID, title string) (bookingID string, err error)
	CancelInterviewBooking(ctx context.Context, bookingID string) error
}

// RemoteInterviewCalendar talks to calendar.v1.CalendarService.
type RemoteInterviewCalendar struct {
	Client calendarv1connect.CalendarServiceClient
}

func (r *RemoteInterviewCalendar) client() (calendarv1connect.CalendarServiceClient, error) {
	if r == nil || r.Client == nil {
		return nil, fmt.Errorf("%w: calendar client required", models.ErrUnavailable)
	}
	return r.Client, nil
}

func (r *RemoteInterviewCalendar) EnsurePanelResources(ctx context.Context, profileIDs []string) ([]string, error) {
	cli, err := r.client()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(profileIDs))
	for _, pid := range profileIDs {
		if pid == "" {
			continue
		}
		resp, err := cli.EnsureResource(ctx, connect.NewRequest(&calendarv1.EnsureResourceRequest{
			Type: "person", SubjectKind: "profile", SubjectId: pid,
			DisplayName: pid, Timezone: "UTC", Capacity: 1,
		}))
		if err != nil {
			return nil, fmt.Errorf("ats: ensure calendar resource %s: %w", pid, err)
		}
		out = append(out, resp.Msg.GetResource().GetId())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no panel resources", models.ErrInvalid)
	}
	return out, nil
}

func (r *RemoteInterviewCalendar) SyncProfileAvailability(
	ctx context.Context,
	profileID, timezone string,
	rules []models.WeekRule,
	exceptions []models.ExceptionDay,
) error {
	cli, err := r.client()
	if err != nil {
		return err
	}
	ids, err := r.EnsurePanelResources(ctx, []string{profileID})
	if err != nil {
		return err
	}
	prules := make([]*calendarv1.WeekRule, 0, len(rules))
	for _, rule := range rules {
		prules = append(prules, &calendarv1.WeekRule{
			Weekday: int32(rule.Weekday), Start: rule.Start, End: rule.End,
		})
	}
	pex := make([]*calendarv1.ExceptionDay, 0, len(exceptions))
	for _, e := range exceptions {
		pex = append(pex, &calendarv1.ExceptionDay{Date: e.Date, Blocked: e.Blocked})
	}
	if timezone == "" {
		timezone = "UTC"
	}
	_, err = cli.SetAvailability(ctx, connect.NewRequest(&calendarv1.SetAvailabilityRequest{
		ResourceId: ids[0], Timezone: timezone, Rules: prules, Exceptions: pex,
	}))
	if err != nil {
		return fmt.Errorf("ats: calendar SetAvailability: %w", err)
	}
	return nil
}

func (r *RemoteInterviewCalendar) GetProfileAvailability(ctx context.Context, profileID string) (string, []models.WeekRule, []models.ExceptionDay, error) {
	cli, err := r.client()
	if err != nil {
		return "", nil, nil, err
	}
	ids, err := r.EnsurePanelResources(ctx, []string{profileID})
	if err != nil {
		return "", nil, nil, err
	}
	resp, err := cli.GetAvailability(ctx, connect.NewRequest(&calendarv1.GetAvailabilityRequest{
		ResourceId: ids[0],
	}))
	if err != nil {
		return "", nil, nil, fmt.Errorf("ats: calendar GetAvailability: %w", err)
	}
	av := resp.Msg.GetAvailability()
	if av == nil {
		return "UTC", nil, nil, nil
	}
	rules := make([]models.WeekRule, 0, len(av.GetRules()))
	for _, rule := range av.GetRules() {
		rules = append(rules, models.WeekRule{
			Weekday: int(rule.GetWeekday()), Start: rule.GetStart(), End: rule.GetEnd(),
		})
	}
	ex := make([]models.ExceptionDay, 0, len(av.GetExceptions()))
	for _, e := range av.GetExceptions() {
		ex = append(ex, models.ExceptionDay{Date: e.GetDate(), Blocked: e.GetBlocked()})
	}
	tz := av.GetTimezone()
	if tz == "" {
		tz = "UTC"
	}
	return tz, rules, ex, nil
}

func (r *RemoteInterviewCalendar) ListPanelSlots(
	ctx context.Context,
	resourceIDs []string,
	durationMin int,
	windowStart, windowEnd time.Time,
) ([]models.Slot, error) {
	cli, err := r.client()
	if err != nil {
		return nil, err
	}
	req := &calendarv1.ListSlotsRequest{
		ResourceIds: resourceIDs,
		DurationMin: int32(durationMin),
	}
	if !windowStart.IsZero() {
		req.WindowStart = windowStart.UTC().Format(time.RFC3339)
	}
	if !windowEnd.IsZero() {
		req.WindowEnd = windowEnd.UTC().Format(time.RFC3339)
	}
	resp, err := cli.ListSlots(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, fmt.Errorf("ats: calendar ListSlots: %w", err)
	}
	out := make([]models.Slot, 0, len(resp.Msg.GetSlots()))
	for _, sl := range resp.Msg.GetSlots() {
		st, e1 := time.Parse(time.RFC3339, sl.GetStart())
		en, e2 := time.Parse(time.RFC3339, sl.GetEnd())
		if e1 != nil || e2 != nil {
			continue
		}
		out = append(out, models.Slot{Start: st, End: en})
	}
	return out, nil
}

func (r *RemoteInterviewCalendar) BookPanel(
	ctx context.Context,
	resourceIDs []string,
	start, end time.Time,
	interviewID, title string,
) (string, error) {
	cli, err := r.client()
	if err != nil {
		return "", err
	}
	lines := make([]*calendarv1.BookingLine, 0, len(resourceIDs))
	for _, id := range resourceIDs {
		lines = append(lines, &calendarv1.BookingLine{ResourceId: id, Quantity: 1})
	}
	if title == "" {
		title = "Interview"
	}
	resp, err := cli.CreateBooking(ctx, connect.NewRequest(&calendarv1.CreateBookingRequest{
		Lines:  lines,
		Start:  start.UTC().Format(time.RFC3339),
		End:    end.UTC().Format(time.RFC3339),
		Status: "confirmed",
		Source: "ats", SourceRef: "interview:" + interviewID,
		Title:          title,
		IdempotencyKey: "ats_interview_" + interviewID,
	}))
	if err != nil {
		return "", fmt.Errorf("ats: calendar CreateBooking: %w", err)
	}
	return resp.Msg.GetBooking().GetId(), nil
}

func (r *RemoteInterviewCalendar) CancelInterviewBooking(ctx context.Context, bookingID string) error {
	if bookingID == "" {
		return nil
	}
	cli, err := r.client()
	if err != nil {
		return err
	}
	_, err = cli.CancelBooking(ctx, connect.NewRequest(&calendarv1.CancelBookingRequest{
		Id: bookingID, Reason: "ats cancel/reschedule",
	}))
	if err != nil {
		return fmt.Errorf("ats: calendar CancelBooking: %w", err)
	}
	return nil
}

// availabilityJSON helpers for local cache mirror (optional).
func marshalRules(rules []models.WeekRule, ex []models.ExceptionDay) (string, string) {
	rj, _ := json.Marshal(rules)
	ej, _ := json.Marshal(ex)
	return string(rj), string(ej)
}
