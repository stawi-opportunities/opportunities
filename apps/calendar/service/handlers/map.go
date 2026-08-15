package handlers

import (
	"encoding/json"
	"time"

	calendarv1 "github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

func resourceProto(r *models.Resource) *calendarv1.Resource {
	if r == nil {
		return nil
	}
	return &calendarv1.Resource{
		Id: r.ID, TenantId: r.TenantID, PartitionId: r.PartitionID,
		Type: r.Type, SubjectKind: r.SubjectKind, SubjectId: r.SubjectID,
		DisplayName: r.DisplayName, Timezone: r.Timezone, Capacity: int32(r.Capacity),
		Status: r.Status, MetadataJson: r.MetadataJSON,
	}
}

func availabilityProto(a *models.Availability) *calendarv1.Availability {
	if a == nil {
		return nil
	}
	var rules []models.WeekRule
	_ = json.Unmarshal([]byte(a.RulesJSON), &rules)
	var ex []models.ExceptionDay
	_ = json.Unmarshal([]byte(a.ExceptionsJSON), &ex)
	out := &calendarv1.Availability{
		ResourceId: a.ResourceID, Timezone: a.Timezone,
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, &calendarv1.WeekRule{
			Weekday: int32(r.Weekday), Start: r.Start, End: r.End,
		})
	}
	for _, e := range ex {
		out.Exceptions = append(out.Exceptions, &calendarv1.ExceptionDay{
			Date: e.Date, Blocked: e.Blocked,
		})
	}
	return out
}

func bookingProto(b *models.Booking, lines []*models.BookingLine) *calendarv1.Booking {
	if b == nil {
		return nil
	}
	out := &calendarv1.Booking{
		Id: b.ID, TenantId: b.TenantID, PartitionId: b.PartitionID,
		Status: b.Status, Start: b.StartAt.UTC().Format(time.RFC3339), End: b.EndAt.UTC().Format(time.RFC3339),
		Source: b.Source, SourceRef: b.SourceRef, OrganizerProfileId: b.OrganizerProfileID,
		Title: b.Title, Description: b.Description, Location: b.Location,
		IdempotencyKey: b.IdempotencyKey, IcsUid: b.ICSUID, MetadataJson: b.MetadataJSON,
	}
	if b.HoldExpiresAt != nil {
		out.HoldExpiresAt = b.HoldExpiresAt.UTC().Format(time.RFC3339)
	}
	for _, ln := range lines {
		out.Lines = append(out.Lines, &calendarv1.BookingLine{
			ResourceId: ln.ResourceID, Quantity: int32(ln.Quantity), ExternalEventId: ln.ExternalEventID,
		})
	}
	return out
}

func connectionProto(c *models.ExternalConnection) *calendarv1.ExternalConnection {
	if c == nil {
		return nil
	}
	out := &calendarv1.ExternalConnection{
		Id: c.ID, ResourceId: c.ResourceID, Provider: c.Provider,
		ExternalCalendarId: c.ExternalCalendarID, ImportBusy: c.ImportBusy,
		ExportBookings: c.ExportBookings, Status: c.Status, LastError: c.LastError,
	}
	if c.LastSyncAt != nil {
		out.LastSyncAt = c.LastSyncAt.UTC().Format(time.RFC3339)
	}
	return out
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}
