package handlers

import (
	"context"
	"time"

	"connectrpc.com/connect"

	calendarv1 "github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1"
	"github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1/calendarv1connect"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/business"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

type ConnectServer struct {
	calendarv1connect.UnimplementedCalendarServiceHandler
	svc *business.Service
}

func NewConnectServer(svc *business.Service) *ConnectServer {
	return &ConnectServer{svc: svc}
}

func (s *ConnectServer) EnsureResource(ctx context.Context, req *connect.Request[calendarv1.EnsureResourceRequest]) (*connect.Response[calendarv1.EnsureResourceResponse], error) {
	r, err := s.svc.EnsureResource(ctx, business.EnsureResourceInput{
		Type: req.Msg.GetType(), SubjectKind: req.Msg.GetSubjectKind(), SubjectID: req.Msg.GetSubjectId(),
		DisplayName: req.Msg.GetDisplayName(), Timezone: req.Msg.GetTimezone(),
		Capacity: int(req.Msg.GetCapacity()), MetadataJSON: req.Msg.GetMetadataJson(),
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.EnsureResourceResponse{Resource: resourceProto(r)}), nil
}

func (s *ConnectServer) GetResource(ctx context.Context, req *connect.Request[calendarv1.GetResourceRequest]) (*connect.Response[calendarv1.GetResourceResponse], error) {
	r, err := s.svc.GetResource(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.GetResourceResponse{Resource: resourceProto(r)}), nil
}

func (s *ConnectServer) ListResources(ctx context.Context, req *connect.Request[calendarv1.ListResourcesRequest]) (*connect.Response[calendarv1.ListResourcesResponse], error) {
	rows, err := s.svc.ListResources(ctx, req.Msg.GetType(), req.Msg.GetSubjectKind(), req.Msg.GetSubjectId(), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapConnectErr(err)
	}
	out := make([]*calendarv1.Resource, 0, len(rows))
	for _, r := range rows {
		out = append(out, resourceProto(r))
	}
	return connect.NewResponse(&calendarv1.ListResourcesResponse{Resources: out}), nil
}

func (s *ConnectServer) SetAvailability(ctx context.Context, req *connect.Request[calendarv1.SetAvailabilityRequest]) (*connect.Response[calendarv1.SetAvailabilityResponse], error) {
	rules := make([]models.WeekRule, 0, len(req.Msg.GetRules()))
	for _, r := range req.Msg.GetRules() {
		rules = append(rules, models.WeekRule{Weekday: int(r.GetWeekday()), Start: r.GetStart(), End: r.GetEnd()})
	}
	ex := make([]models.ExceptionDay, 0, len(req.Msg.GetExceptions()))
	for _, e := range req.Msg.GetExceptions() {
		ex = append(ex, models.ExceptionDay{Date: e.GetDate(), Blocked: e.GetBlocked()})
	}
	a, err := s.svc.SetAvailability(ctx, business.SetAvailabilityInput{
		ResourceID: req.Msg.GetResourceId(), Timezone: req.Msg.GetTimezone(), Rules: rules, Exceptions: ex,
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.SetAvailabilityResponse{Availability: availabilityProto(a)}), nil
}

func (s *ConnectServer) GetAvailability(ctx context.Context, req *connect.Request[calendarv1.GetAvailabilityRequest]) (*connect.Response[calendarv1.GetAvailabilityResponse], error) {
	a, err := s.svc.GetAvailability(ctx, req.Msg.GetResourceId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.GetAvailabilityResponse{Availability: availabilityProto(a)}), nil
}

func (s *ConnectServer) ListSlots(ctx context.Context, req *connect.Request[calendarv1.ListSlotsRequest]) (*connect.Response[calendarv1.ListSlotsResponse], error) {
	ws, err := parseTime(req.Msg.GetWindowStart())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	we, err := parseTime(req.Msg.GetWindowEnd())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	slots, err := s.svc.ListSlots(ctx, req.Msg.GetResourceIds(), int(req.Msg.GetDurationMin()), ws, we)
	if err != nil {
		return nil, mapConnectErr(err)
	}
	out := make([]*calendarv1.Slot, 0, len(slots))
	for _, sl := range slots {
		out = append(out, &calendarv1.Slot{
			Start: sl.Start.UTC().Format(time.RFC3339), End: sl.End.UTC().Format(time.RFC3339),
		})
	}
	return connect.NewResponse(&calendarv1.ListSlotsResponse{Slots: out}), nil
}

func (s *ConnectServer) ListBusy(ctx context.Context, req *connect.Request[calendarv1.ListBusyRequest]) (*connect.Response[calendarv1.ListBusyResponse], error) {
	ws, err := parseTime(req.Msg.GetWindowStart())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	we, err := parseTime(req.Msg.GetWindowEnd())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rows, err := s.svc.ListBusy(ctx, req.Msg.GetResourceIds(), ws, we)
	if err != nil {
		return nil, mapConnectErr(err)
	}
	out := make([]*calendarv1.BusyInterval, 0, len(rows))
	for _, b := range rows {
		out = append(out, &calendarv1.BusyInterval{
			ResourceId: b.ResourceID, Start: b.Start.UTC().Format(time.RFC3339),
			End: b.End.UTC().Format(time.RFC3339), Source: b.Source, Note: b.Note,
		})
	}
	return connect.NewResponse(&calendarv1.ListBusyResponse{Intervals: out}), nil
}

func (s *ConnectServer) CreateBooking(ctx context.Context, req *connect.Request[calendarv1.CreateBookingRequest]) (*connect.Response[calendarv1.CreateBookingResponse], error) {
	start, err := parseTime(req.Msg.GetStart())
	if err != nil || start.IsZero() {
		return nil, connect.NewError(connect.CodeInvalidArgument, models.ErrInvalid)
	}
	end, err := parseTime(req.Msg.GetEnd())
	if err != nil || end.IsZero() {
		return nil, connect.NewError(connect.CodeInvalidArgument, models.ErrInvalid)
	}
	lines := make([]models.BookingLine, 0, len(req.Msg.GetLines()))
	for _, ln := range req.Msg.GetLines() {
		lines = append(lines, models.BookingLine{
			ResourceID: ln.GetResourceId(), Quantity: int(ln.GetQuantity()),
		})
	}
	b, created, err := s.svc.CreateBooking(ctx, business.CreateBookingInput{
		Lines: lines, Start: start, End: end, Status: req.Msg.GetStatus(),
		HoldTTLSeconds: int(req.Msg.GetHoldTtlSeconds()),
		Source:         req.Msg.GetSource(), SourceRef: req.Msg.GetSourceRef(),
		OrganizerProfileID: req.Msg.GetOrganizerProfileId(),
		Title:              req.Msg.GetTitle(), Description: req.Msg.GetDescription(), Location: req.Msg.GetLocation(),
		IdempotencyKey: req.Msg.GetIdempotencyKey(), MetadataJSON: req.Msg.GetMetadataJson(),
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.CreateBookingResponse{Booking: bookingProto(b, created)}), nil
}

func (s *ConnectServer) ConfirmBooking(ctx context.Context, req *connect.Request[calendarv1.ConfirmBookingRequest]) (*connect.Response[calendarv1.ConfirmBookingResponse], error) {
	b, lines, err := s.svc.ConfirmBooking(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.ConfirmBookingResponse{Booking: bookingProto(b, lines)}), nil
}

func (s *ConnectServer) CancelBooking(ctx context.Context, req *connect.Request[calendarv1.CancelBookingRequest]) (*connect.Response[calendarv1.CancelBookingResponse], error) {
	b, lines, err := s.svc.CancelBooking(ctx, req.Msg.GetId(), req.Msg.GetReason())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.CancelBookingResponse{Booking: bookingProto(b, lines)}), nil
}

func (s *ConnectServer) GetBooking(ctx context.Context, req *connect.Request[calendarv1.GetBookingRequest]) (*connect.Response[calendarv1.GetBookingResponse], error) {
	b, lines, err := s.svc.GetBooking(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.GetBookingResponse{Booking: bookingProto(b, lines)}), nil
}

func (s *ConnectServer) GetBookingICS(ctx context.Context, req *connect.Request[calendarv1.GetBookingICSRequest]) (*connect.Response[calendarv1.GetBookingICSResponse], error) {
	ics, err := s.svc.GetBookingICS(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.GetBookingICSResponse{Ics: ics}), nil
}

func (s *ConnectServer) UpsertExternalConnection(ctx context.Context, req *connect.Request[calendarv1.UpsertExternalConnectionRequest]) (*connect.Response[calendarv1.UpsertExternalConnectionResponse], error) {
	c, err := s.svc.UpsertExternalConnection(ctx, business.UpsertConnectionInput{
		ResourceID: req.Msg.GetResourceId(), Provider: req.Msg.GetProvider(),
		ExternalCalendarID: req.Msg.GetExternalCalendarId(), CredentialsJSON: req.Msg.GetCredentialsJson(),
		ImportBusy: req.Msg.GetImportBusy(), ExportBookings: req.Msg.GetExportBookings(),
	})
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.UpsertExternalConnectionResponse{Connection: connectionProto(c)}), nil
}

func (s *ConnectServer) TriggerSync(ctx context.Context, req *connect.Request[calendarv1.TriggerSyncRequest]) (*connect.Response[calendarv1.TriggerSyncResponse], error) {
	res, err := s.svc.TriggerSync(ctx, req.Msg.GetConnectionId(), req.Msg.GetImportBusy(), req.Msg.GetExportPending())
	if err != nil {
		return nil, mapConnectErr(err)
	}
	return connect.NewResponse(&calendarv1.TriggerSyncResponse{
		ImportedBlocks: int32(res.Imported), ExportedBookings: int32(res.Exported), Errors: int32(res.Errors),
	}), nil
}
