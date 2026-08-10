package business

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
	calendarv1 "github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1"
	"github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1/calendarv1connect"
)

// InterviewCalendar is an optional reservation plane (service_calendar).
// When nil, ATS uses local availability + interview busy only.
type InterviewCalendar interface {
	// EnsurePanelResources ensures person resources for profile IDs and returns resource IDs in order.
	EnsurePanelResources(ctx context.Context, profileIDs []string) (resourceIDs []string, err error)
	// SyncProfileAvailability pushes ATS weekly rules to the calendar resource for a profile.
	SyncProfileAvailability(ctx context.Context, profileID, timezone string, rules []models.WeekRule, exceptions []models.ExceptionDay) error
	// ListPanelSlots returns free slots for the panel (and optional extra resource IDs).
	ListPanelSlots(ctx context.Context, resourceIDs []string, durationMin int, windowStart, windowEnd time.Time) ([]models.Slot, error)
	// BookPanel reserves the panel for an interview (source=ats).
	BookPanel(ctx context.Context, resourceIDs []string, start, end time.Time, interviewID, title string) (bookingID string, err error)
	// CancelInterviewBooking cancels the calendar booking if known.
	CancelInterviewBooking(ctx context.Context, bookingID string) error
}

// RemoteInterviewCalendar talks to calendar.v1.CalendarService.
type RemoteInterviewCalendar struct {
	Client calendarv1connect.CalendarServiceClient
}

func (r *RemoteInterviewCalendar) EnsurePanelResources(ctx context.Context, profileIDs []string) ([]string, error) {
	if r == nil || r.Client == nil {
		return nil, fmt.Errorf("ats: calendar client nil")
	}
	out := make([]string, 0, len(profileIDs))
	for _, pid := range profileIDs {
		if pid == "" {
			continue
		}
		resp, err := r.Client.EnsureResource(ctx, connect.NewRequest(&calendarv1.EnsureResourceRequest{
			Type: "person", SubjectKind: "profile", SubjectId: pid,
			DisplayName: pid, Timezone: "UTC", Capacity: 1,
		}))
		if err != nil {
			return nil, fmt.Errorf("ats: ensure calendar resource %s: %w", pid, err)
		}
		out = append(out, resp.Msg.GetResource().GetId())
	}
	return out, nil
}

func (r *RemoteInterviewCalendar) SyncProfileAvailability(
	ctx context.Context,
	profileID, timezone string,
	rules []models.WeekRule,
	exceptions []models.ExceptionDay,
) error {
	ids, err := r.EnsurePanelResources(ctx, []string{profileID})
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("ats: no calendar resource for profile")
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
	_, err = r.Client.SetAvailability(ctx, connect.NewRequest(&calendarv1.SetAvailabilityRequest{
		ResourceId: ids[0], Timezone: timezone, Rules: prules, Exceptions: pex,
	}))
	return err
}

func (r *RemoteInterviewCalendar) ListPanelSlots(
	ctx context.Context,
	resourceIDs []string,
	durationMin int,
	windowStart, windowEnd time.Time,
) ([]models.Slot, error) {
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
	resp, err := r.Client.ListSlots(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
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
	lines := make([]*calendarv1.BookingLine, 0, len(resourceIDs))
	for _, id := range resourceIDs {
		lines = append(lines, &calendarv1.BookingLine{ResourceId: id, Quantity: 1})
	}
	if title == "" {
		title = "Interview"
	}
	resp, err := r.Client.CreateBooking(ctx, connect.NewRequest(&calendarv1.CreateBookingRequest{
		Lines:  lines,
		Start:  start.UTC().Format(time.RFC3339),
		End:    end.UTC().Format(time.RFC3339),
		Status: "confirmed",
		Source: "ats", SourceRef: "interview:" + interviewID,
		Title:          title,
		IdempotencyKey: "ats_interview_" + interviewID,
	}))
	if err != nil {
		return "", err
	}
	return resp.Msg.GetBooking().GetId(), nil
}

func (r *RemoteInterviewCalendar) CancelInterviewBooking(ctx context.Context, bookingID string) error {
	if bookingID == "" {
		return nil
	}
	_, err := r.Client.CancelBooking(ctx, connect.NewRequest(&calendarv1.CancelBookingRequest{
		Id: bookingID, Reason: "ats cancel/reschedule",
	}))
	return err
}

// Soft-fail helper for dual-write availability.
func syncAvailabilitySoft(ctx context.Context, cal InterviewCalendar, profileID, tz string, rules []models.WeekRule, ex []models.ExceptionDay) {
	if cal == nil {
		return
	}
	if err := cal.SyncProfileAvailability(ctx, profileID, tz, rules, ex); err != nil {
		util.Log(ctx).WithError(err).WithField("profile_id", profileID).
			Warn("ats: calendar availability sync failed; local ATS availability remains SoT")
	}
}
