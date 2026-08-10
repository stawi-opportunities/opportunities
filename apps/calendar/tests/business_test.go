package tests

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/stawi-opportunities/opportunities/apps/calendar/service/business"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

type BusinessSuite struct {
	CalendarBaseTestSuite
}

func TestBusinessSuite(t *testing.T) {
	suite.Run(t, new(BusinessSuite))
}

func weekdays9to5() []models.WeekRule {
	var rules []models.WeekRule
	for d := 1; d <= 5; d++ {
		rules = append(rules, models.WeekRule{Weekday: d, Start: "09:00", End: "17:00"})
	}
	return rules
}

func (s *BusinessSuite) TestResourceSlotsBookConflictAndICS() {
	t := s.T()
	ctx, deps := s.CreateService(t)
	actor := ClaimsContext(ctx, "t1", "p1", "user-1")

	// Person + room
	person, err := deps.Svc.EnsureResource(actor, business.EnsureResourceInput{
		Type: "person", SubjectKind: "profile", SubjectID: "prof_recruiter",
		DisplayName: "Recruiter", Timezone: "UTC", Capacity: 1,
	})
	require.NoError(t, err)
	room, err := deps.Svc.EnsureResource(actor, business.EnsureResourceInput{
		Type: "room", SubjectKind: "external", SubjectID: "room-3a",
		DisplayName: "Room 3A", Capacity: 1,
	})
	require.NoError(t, err)

	// Equipment with capacity 2
	kit, err := deps.Svc.EnsureResource(actor, business.EnsureResourceInput{
		Type: "equipment", SubjectKind: "inventory", SubjectID: "cam-kit",
		DisplayName: "Camera kit", Capacity: 2,
	})
	require.NoError(t, err)

	for _, id := range []string{person.ID, room.ID, kit.ID} {
		_, err = deps.Svc.SetAvailability(actor, business.SetAvailabilityInput{
			ResourceID: id, Timezone: "UTC", Rules: weekdays9to5(),
		})
		require.NoError(t, err)
	}

	// Multi-resource slots
	slots, err := deps.Svc.ListSlots(actor, []string{person.ID, room.ID, kit.ID}, 30, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.NotEmpty(t, slots)

	// Book exclusive person+room
	b, lines, err := deps.Svc.CreateBooking(actor, business.CreateBookingInput{
		Lines: []models.BookingLine{
			{ResourceID: person.ID, Quantity: 1},
			{ResourceID: room.ID, Quantity: 1},
		},
		Start: slots[0].Start, End: slots[0].End,
		Status: models.BookingConfirmed, Source: "ats", SourceRef: "interview:1",
		Title: "Interview", IdempotencyKey: "book-1",
	})
	require.NoError(t, err)
	require.Len(t, lines, 2)
	require.Equal(t, models.BookingConfirmed, b.Status)

	// Idempotent replay
	b2, _, err := deps.Svc.CreateBooking(actor, business.CreateBookingInput{
		Lines: []models.BookingLine{{ResourceID: person.ID}},
		Start: slots[0].Start, End: slots[0].End,
		IdempotencyKey: "book-1",
	})
	require.NoError(t, err)
	require.Equal(t, b.ID, b2.ID)

	// Conflict on same person slot
	_, _, err = deps.Svc.CreateBooking(actor, business.CreateBookingInput{
		Lines: []models.BookingLine{{ResourceID: person.ID}},
		Start: slots[0].Start, End: slots[0].End,
		IdempotencyKey: "book-2",
	})
	require.ErrorIs(t, err, models.ErrConflict)

	// Capacity: kit can take two concurrent
	_, _, err = deps.Svc.CreateBooking(actor, business.CreateBookingInput{
		Lines: []models.BookingLine{{ResourceID: kit.ID, Quantity: 1}},
		Start: slots[0].Start, End: slots[0].End, IdempotencyKey: "kit-1",
	})
	require.NoError(t, err)
	_, _, err = deps.Svc.CreateBooking(actor, business.CreateBookingInput{
		Lines: []models.BookingLine{{ResourceID: kit.ID, Quantity: 1}},
		Start: slots[0].Start, End: slots[0].End, IdempotencyKey: "kit-2",
	})
	require.NoError(t, err)
	_, _, err = deps.Svc.CreateBooking(actor, business.CreateBookingInput{
		Lines: []models.BookingLine{{ResourceID: kit.ID, Quantity: 1}},
		Start: slots[0].Start, End: slots[0].End, IdempotencyKey: "kit-3",
	})
	require.ErrorIs(t, err, models.ErrConflict)

	ics, err := deps.Svc.GetBookingICS(actor, b.ID)
	require.NoError(t, err)
	require.Contains(t, ics, "BEGIN:VCALENDAR")
}

func (s *BusinessSuite) TestHoldConfirmAndExternalSync() {
	t := s.T()
	ctx, deps := s.CreateService(t)
	actor := ClaimsContext(ctx, "t1", "p1", "user-1")

	res, err := deps.Svc.EnsureResource(actor, business.EnsureResourceInput{
		Type: "person", SubjectKind: "profile", SubjectID: "prof_a", Timezone: "UTC",
	})
	require.NoError(t, err)
	_, err = deps.Svc.SetAvailability(actor, business.SetAvailabilityInput{
		ResourceID: res.ID, Timezone: "UTC", Rules: weekdays9to5(),
	})
	require.NoError(t, err)

	slots, err := deps.Svc.ListSlots(actor, []string{res.ID}, 30, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.NotEmpty(t, slots)

	// Wire external connection first so confirmed bookings export.
	conn, err := deps.Svc.UpsertExternalConnection(actor, business.UpsertConnectionInput{
		ResourceID: res.ID, Provider: "memory",
		ExternalCalendarID: "primary", ImportBusy: true, ExportBookings: true,
	})
	require.NoError(t, err)

	hold, _, err := deps.Svc.CreateBooking(actor, business.CreateBookingInput{
		Lines: []models.BookingLine{{ResourceID: res.ID}},
		Start: slots[0].Start, End: slots[0].End,
		Status: models.BookingHold, HoldTTLSeconds: 600, IdempotencyKey: "hold-1",
	})
	require.NoError(t, err)
	require.Equal(t, models.BookingHold, hold.Status)

	// Second hold same slot conflicts
	_, _, err = deps.Svc.CreateBooking(actor, business.CreateBookingInput{
		Lines: []models.BookingLine{{ResourceID: res.ID}},
		Start: slots[0].Start, End: slots[0].End,
		Status: models.BookingHold, IdempotencyKey: "hold-2",
	})
	require.ErrorIs(t, err, models.ErrConflict)

	confirmed, _, err := deps.Svc.ConfirmBooking(actor, hold.ID)
	require.NoError(t, err)
	require.Equal(t, models.BookingConfirmed, confirmed.Status)

	sent, errs := deps.Svc.DrainExportOutbox(ctx, 20)
	require.Equal(t, 0, errs)
	require.GreaterOrEqual(t, sent, 1)
	require.NotEmpty(t, deps.Memory.Events)

	// Import busy from memory
	resSync, err := deps.Svc.TriggerSync(actor, conn.ID, true, false)
	require.NoError(t, err)
	require.GreaterOrEqual(t, resSync.Imported, 1)

	_, _, err = deps.Svc.CancelBooking(actor, confirmed.ID, "reschedule")
	require.NoError(t, err)
}
