package business

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/repository"
)

// Scope is tenant+partition+actor from JWT claims.
type Scope struct {
	TenantID    string
	PartitionID string
	ProfileID   string
}

func ScopeFromContext(ctx context.Context) (Scope, error) {
	c := security.ClaimsFromContext(ctx)
	if c == nil {
		return Scope{}, fmt.Errorf("%w: missing claims", models.ErrForbidden)
	}
	s := Scope{
		TenantID:    c.GetTenantID(),
		PartitionID: c.GetPartitionID(),
		ProfileID:   c.GetProfileID(),
	}
	if s.ProfileID == "" {
		s.ProfileID = c.Subject
	}
	if s.TenantID == "" || s.PartitionID == "" {
		return Scope{}, fmt.Errorf("%w: tenant_id and partition_id required", models.ErrForbidden)
	}
	return s, nil
}

// Deps are repository + provider ports.
type Deps struct {
	Resources      repository.ResourceRepository
	Availability   repository.AvailabilityRepository
	Busy           repository.BusyRepository
	Bookings       repository.BookingRepository
	Lines          repository.BookingLineRepository
	Connections    repository.ExternalConnectionRepository
	SyncOutbox     repository.SyncOutboxRepository
	Providers      ProviderRegistry
	SlotWindowDays int
}

// Service is the calendar business layer.
type Service struct {
	Deps
}

func NewService(d Deps) *Service {
	if d.Providers == nil {
		d.Providers = ProviderRegistry{}
	}
	// Always register integration-ready stubs so UpsertExternalConnection
	// accepts known provider names; Ready() gates live sync.
	if d.Providers.Get(models.ProviderGoogle) == nil {
		d.Providers[models.ProviderGoogle] = NoopProvider{ProviderName: models.ProviderGoogle}
	}
	if d.Providers.Get(models.ProviderMicrosoft) == nil {
		d.Providers[models.ProviderMicrosoft] = NoopProvider{ProviderName: models.ProviderMicrosoft}
	}
	if d.Providers.Get(models.ProviderCalDAV) == nil {
		d.Providers[models.ProviderCalDAV] = NoopProvider{ProviderName: models.ProviderCalDAV}
	}
	if d.SlotWindowDays <= 0 {
		d.SlotWindowDays = 14
	}
	return &Service{Deps: d}
}

// EnsureResourceInput creates or returns a resource by subject.
type EnsureResourceInput struct {
	Type, SubjectKind, SubjectID, DisplayName, Timezone, MetadataJSON string
	Capacity                                                          int
}

func (s *Service) EnsureResource(ctx context.Context, in EnsureResourceInput) (*models.Resource, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if in.Type == "" || in.SubjectKind == "" || in.SubjectID == "" {
		return nil, fmt.Errorf("%w: type, subject_kind, subject_id required", models.ErrInvalid)
	}
	existing, err := s.Resources.GetBySubject(ctx, sc.TenantID, sc.PartitionID, in.SubjectKind, in.SubjectID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		fields := make([]string, 0, 4)
		if in.DisplayName != "" && in.DisplayName != existing.DisplayName {
			existing.DisplayName = in.DisplayName
			fields = append(fields, "display_name")
		}
		if in.Timezone != "" && in.Timezone != existing.Timezone {
			existing.Timezone = in.Timezone
			fields = append(fields, "timezone")
		}
		if in.Capacity > 0 && in.Capacity != existing.Capacity {
			existing.Capacity = in.Capacity
			fields = append(fields, "capacity")
		}
		if in.MetadataJSON != "" && in.MetadataJSON != existing.MetadataJSON {
			existing.MetadataJSON = in.MetadataJSON
			fields = append(fields, "metadata_json")
		}
		if len(fields) > 0 {
			if _, err := s.Resources.Update(ctx, existing, fields...); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}
	cap := in.Capacity
	if cap <= 0 {
		cap = 1
	}
	tz := in.Timezone
	if tz == "" {
		tz = "UTC"
	}
	meta := in.MetadataJSON
	if meta == "" {
		meta = "{}"
	}
	res := &models.Resource{
		Type: in.Type, SubjectKind: in.SubjectKind, SubjectID: in.SubjectID,
		DisplayName: in.DisplayName, Timezone: tz, Capacity: cap,
		Status: models.ResourceActive, MetadataJSON: meta,
	}
	if err := s.Resources.Create(ctx, res); err != nil {
		return nil, fmt.Errorf("calendar: create resource: %w", err)
	}
	return res, nil
}

func (s *Service) GetResource(ctx context.Context, id string) (*models.Resource, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.Resources.GetInPartition(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, models.ErrNotFound
	}
	return res, nil
}

func (s *Service) ListResources(ctx context.Context, typ, subjectKind, subjectID string, limit int) ([]*models.Resource, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.Resources.List(ctx, sc.TenantID, sc.PartitionID, typ, subjectKind, subjectID, limit)
}

type SetAvailabilityInput struct {
	ResourceID string
	Timezone   string
	Rules      []models.WeekRule
	Exceptions []models.ExceptionDay
}

func (s *Service) SetAvailability(ctx context.Context, in SetAvailabilityInput) (*models.Availability, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.GetResource(ctx, in.ResourceID); err != nil {
		return nil, err
	}
	tz := in.Timezone
	if tz == "" {
		tz = "UTC"
	}
	rj, _ := json.Marshal(in.Rules)
	ej, _ := json.Marshal(in.Exceptions)
	a := &models.Availability{
		ResourceID: in.ResourceID, Timezone: tz,
		RulesJSON: string(rj), ExceptionsJSON: string(ej),
	}
	a.TenantID = sc.TenantID
	a.PartitionID = sc.PartitionID
	if err := s.Availability.UpsertForResource(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) GetAvailability(ctx context.Context, resourceID string) (*models.Availability, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.GetResource(ctx, resourceID); err != nil {
		return nil, err
	}
	return s.Availability.GetByResource(ctx, sc.TenantID, sc.PartitionID, resourceID)
}

func (s *Service) ListSlots(ctx context.Context, resourceIDs []string, durationMin int, windowStart, windowEnd time.Time) ([]models.Slot, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if len(resourceIDs) == 0 {
		return nil, fmt.Errorf("%w: resource_ids required", models.ErrInvalid)
	}
	if durationMin <= 0 {
		durationMin = 30
	}
	now := time.Now().UTC()
	if windowStart.IsZero() {
		windowStart = now
	}
	if windowEnd.IsZero() {
		windowEnd = now.AddDate(0, 0, s.SlotWindowDays)
	}
	demands, err := s.buildDemands(ctx, sc, resourceIDs, nil)
	if err != nil {
		return nil, err
	}
	busyMap, err := s.busyMap(ctx, sc, resourceIDs, windowStart, windowEnd)
	if err != nil {
		return nil, err
	}
	return models.ComputeSlots(demands, busyMap, windowStart, windowEnd, durationMin)
}

func (s *Service) ListBusy(ctx context.Context, resourceIDs []string, from, to time.Time) ([]models.BusyInterval, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return nil, fmt.Errorf("%w: invalid window", models.ErrInvalid)
	}
	m, err := s.busyMap(ctx, sc, resourceIDs, from, to)
	if err != nil {
		return nil, err
	}
	var out []models.BusyInterval
	for _, list := range m {
		out = append(out, list...)
	}
	return out, nil
}

type CreateBookingInput struct {
	Lines                        []models.BookingLine
	Start, End                   time.Time
	Status                       string
	HoldTTLSeconds               int
	Source, SourceRef            string
	OrganizerProfileID           string
	Title, Description, Location string
	IdempotencyKey               string
	MetadataJSON                 string
}

func (s *Service) CreateBooking(ctx context.Context, in CreateBookingInput) (*models.Booking, []*models.BookingLine, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(in.Lines) == 0 {
		return nil, nil, fmt.Errorf("%w: lines required", models.ErrInvalid)
	}
	if !in.End.After(in.Start) {
		return nil, nil, fmt.Errorf("%w: end must be after start", models.ErrInvalid)
	}
	if in.IdempotencyKey != "" {
		if existing, err := s.Bookings.GetByIdempotency(ctx, sc.TenantID, sc.PartitionID, in.IdempotencyKey); err != nil {
			return nil, nil, err
		} else if existing != nil {
			lines, err := s.Lines.ListByBooking(ctx, existing.ID)
			return existing, lines, err
		}
	}
	status := in.Status
	if status == "" {
		status = models.BookingConfirmed
	}
	if status != models.BookingHold && status != models.BookingConfirmed {
		return nil, nil, fmt.Errorf("%w: status must be hold or confirmed", models.ErrInvalid)
	}

	resourceIDs := make([]string, 0, len(in.Lines))
	qty := map[string]int{}
	for i := range in.Lines {
		if in.Lines[i].ResourceID == "" {
			return nil, nil, fmt.Errorf("%w: resource_id required on line", models.ErrInvalid)
		}
		if in.Lines[i].Quantity <= 0 {
			in.Lines[i].Quantity = 1
		}
		resourceIDs = append(resourceIDs, in.Lines[i].ResourceID)
		qty[in.Lines[i].ResourceID] += in.Lines[i].Quantity
	}
	demands, err := s.buildDemands(ctx, sc, resourceIDs, qty)
	if err != nil {
		return nil, nil, err
	}
	busyMap, err := s.busyMap(ctx, sc, resourceIDs, in.Start, in.End)
	if err != nil {
		return nil, nil, err
	}
	if !models.HasCapacity(demands, busyMap, in.Start, in.End) {
		return nil, nil, fmt.Errorf("%w: slot not available", models.ErrConflict)
	}

	b := &models.Booking{
		Status: status, StartAt: in.Start.UTC(), EndAt: in.End.UTC(),
		Source: in.Source, SourceRef: in.SourceRef,
		OrganizerProfileID: in.OrganizerProfileID,
		Title:              in.Title, Description: in.Description, Location: in.Location,
		IdempotencyKey: in.IdempotencyKey, ICSUID: util.IDString(),
		MetadataJSON: in.MetadataJSON,
	}
	if b.MetadataJSON == "" {
		b.MetadataJSON = "{}"
	}
	if b.OrganizerProfileID == "" {
		b.OrganizerProfileID = sc.ProfileID
	}
	if status == models.BookingHold {
		ttl := in.HoldTTLSeconds
		if ttl <= 0 {
			ttl = 300
		}
		exp := time.Now().UTC().Add(time.Duration(ttl) * time.Second)
		b.HoldExpiresAt = &exp
	}
	if err := s.Bookings.Create(ctx, b); err != nil {
		return nil, nil, fmt.Errorf("calendar: create booking: %w", err)
	}
	created := make([]*models.BookingLine, 0, len(in.Lines))
	for _, ln := range in.Lines {
		row := &models.BookingLine{
			BookingID: b.ID, ResourceID: ln.ResourceID, Quantity: ln.Quantity,
		}
		if err := s.Lines.Create(ctx, row); err != nil {
			return nil, nil, fmt.Errorf("calendar: create line: %w", err)
		}
		created = append(created, row)
	}
	if status == models.BookingConfirmed {
		s.enqueueExports(ctx, sc, b, created)
	}
	return b, created, nil
}

func (s *Service) ConfirmBooking(ctx context.Context, id string) (*models.Booking, []*models.BookingLine, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.Bookings.GetInPartition(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, nil, err
	}
	if b == nil {
		return nil, nil, models.ErrNotFound
	}
	if b.Status == models.BookingConfirmed {
		lines, err := s.Lines.ListByBooking(ctx, b.ID)
		return b, lines, err
	}
	if b.Status != models.BookingHold {
		return nil, nil, fmt.Errorf("%w: booking not holdable", models.ErrInvalid)
	}
	if b.HoldExpiresAt != nil && b.HoldExpiresAt.Before(time.Now().UTC()) {
		return nil, nil, fmt.Errorf("%w: hold expired", models.ErrConflict)
	}
	// Re-check capacity excluding this hold.
	lines, err := s.Lines.ListByBooking(ctx, b.ID)
	if err != nil {
		return nil, nil, err
	}
	resourceIDs := make([]string, 0, len(lines))
	qty := map[string]int{}
	for _, ln := range lines {
		resourceIDs = append(resourceIDs, ln.ResourceID)
		qty[ln.ResourceID] += ln.Quantity
	}
	demands, err := s.buildDemands(ctx, sc, resourceIDs, qty)
	if err != nil {
		return nil, nil, err
	}
	busyMap, err := s.busyMapExcludingBooking(ctx, sc, resourceIDs, b.StartAt, b.EndAt, b.ID)
	if err != nil {
		return nil, nil, err
	}
	if !models.HasCapacity(demands, busyMap, b.StartAt, b.EndAt) {
		return nil, nil, fmt.Errorf("%w: slot no longer available", models.ErrConflict)
	}
	b.Status = models.BookingConfirmed
	b.HoldExpiresAt = nil
	if _, err := s.Bookings.Update(ctx, b, "status", "hold_expires_at", "modified_at", "modified_by"); err != nil {
		return nil, nil, err
	}
	s.enqueueExports(ctx, sc, b, lines)
	return b, lines, nil
}

func (s *Service) CancelBooking(ctx context.Context, id, reason string) (*models.Booking, []*models.BookingLine, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.Bookings.GetInPartition(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, nil, err
	}
	if b == nil {
		return nil, nil, models.ErrNotFound
	}
	if b.Status == models.BookingCanceled {
		lines, err := s.Lines.ListByBooking(ctx, b.ID)
		return b, lines, err
	}
	b.Status = models.BookingCanceled
	b.CancelReason = reason
	if _, err := s.Bookings.Update(ctx, b, "status", "cancel_reason", "modified_at", "modified_by"); err != nil {
		return nil, nil, err
	}
	lines, err := s.Lines.ListByBooking(ctx, b.ID)
	if err != nil {
		return nil, nil, err
	}
	s.enqueueDeletes(ctx, sc, b, lines)
	return b, lines, nil
}

func (s *Service) GetBooking(ctx context.Context, id string) (*models.Booking, []*models.BookingLine, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.Bookings.GetInPartition(ctx, sc.TenantID, sc.PartitionID, id)
	if err != nil {
		return nil, nil, err
	}
	if b == nil {
		return nil, nil, models.ErrNotFound
	}
	lines, err := s.Lines.ListByBooking(ctx, b.ID)
	return b, lines, err
}

func (s *Service) GetBookingICS(ctx context.Context, id string) (string, error) {
	b, lines, err := s.GetBooking(ctx, id)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(lines))
	for _, ln := range lines {
		if res, _ := s.GetResource(ctx, ln.ResourceID); res != nil {
			n := res.DisplayName
			if n == "" {
				n = res.SubjectID
			}
			names = append(names, n)
		}
	}
	return models.BuildBookingICS(b, names), nil
}

type UpsertConnectionInput struct {
	ResourceID, Provider, ExternalCalendarID, CredentialsJSON string
	ImportBusy, ExportBookings                                bool
}

func (s *Service) UpsertExternalConnection(ctx context.Context, in UpsertConnectionInput) (*models.ExternalConnection, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.GetResource(ctx, in.ResourceID); err != nil {
		return nil, err
	}
	if in.Provider == "" {
		return nil, fmt.Errorf("%w: provider required", models.ErrInvalid)
	}
	// Accept known providers even if not Ready (integration-ready registration).
	existing, err := s.Connections.ListByResource(ctx, sc.TenantID, sc.PartitionID, in.ResourceID)
	if err != nil {
		return nil, err
	}
	for _, c := range existing {
		if c.Provider == in.Provider {
			c.ExternalCalendarID = in.ExternalCalendarID
			if in.CredentialsJSON != "" {
				c.CredentialsJSON = in.CredentialsJSON
			}
			c.ImportBusy = in.ImportBusy
			c.ExportBookings = in.ExportBookings
			c.Status = models.ConnActive
			c.LastError = ""
			if _, err := s.Connections.Update(ctx, c,
				"external_calendar_id", "credentials_json", "import_busy", "export_bookings",
				"status", "last_error", "modified_at", "modified_by"); err != nil {
				return nil, err
			}
			return c, nil
		}
	}
	c := &models.ExternalConnection{
		ResourceID: in.ResourceID, Provider: in.Provider,
		ExternalCalendarID: in.ExternalCalendarID, CredentialsJSON: in.CredentialsJSON,
		ImportBusy: in.ImportBusy, ExportBookings: in.ExportBookings,
		Status: models.ConnActive,
	}
	if err := s.Connections.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// TriggerSync runs import/export for connections in the caller's partition.
type TriggerSyncResult struct {
	Imported, Exported, Errors int
}

func (s *Service) TriggerSync(ctx context.Context, connectionID string, doImport, doExport bool) (TriggerSyncResult, error) {
	sc, err := ScopeFromContext(ctx)
	if err != nil {
		return TriggerSyncResult{}, err
	}
	var conns []*models.ExternalConnection
	if connectionID != "" {
		c, err := s.Connections.GetInPartition(ctx, sc.TenantID, sc.PartitionID, connectionID)
		if err != nil {
			return TriggerSyncResult{}, err
		}
		if c == nil {
			return TriggerSyncResult{}, models.ErrNotFound
		}
		conns = []*models.ExternalConnection{c}
	} else {
		conns, err = s.Connections.ListActive(ctx, sc.TenantID, sc.PartitionID)
		if err != nil {
			return TriggerSyncResult{}, err
		}
	}
	var res TriggerSyncResult
	now := time.Now().UTC()
	from, to := now.AddDate(0, 0, -1), now.AddDate(0, 0, s.SlotWindowDays)
	for _, c := range conns {
		if doImport && c.ImportBusy {
			n, err := s.importBusy(ctx, sc, c, from, to)
			if err != nil {
				res.Errors++
				c.LastError = err.Error()
				c.Status = models.ConnError
				_, _ = s.Connections.Update(ctx, c, "last_error", "status", "modified_at")
				util.Log(ctx).WithError(err).WithField("connection_id", c.ID).Warn("calendar: import busy failed")
			} else {
				res.Imported += n
				c.LastError = ""
				c.Status = models.ConnActive
				ts := now
				c.LastSyncAt = &ts
				_, _ = s.Connections.Update(ctx, c, "last_error", "status", "last_sync_at", "sync_token", "modified_at")
			}
		}
	}
	if doExport {
		n, errN := s.DrainExportOutbox(ctx, 100)
		res.Exported += n
		res.Errors += errN
	}
	return res, nil
}

func (s *Service) importBusy(ctx context.Context, sc Scope, c *models.ExternalConnection, from, to time.Time) (int, error) {
	p := s.Providers.Get(c.Provider)
	if p == nil {
		return 0, fmt.Errorf("calendar: unknown provider %s", c.Provider)
	}
	if !p.Ready() {
		return 0, fmt.Errorf("calendar: provider %s not ready", c.Provider)
	}
	blocks, token, err := p.ImportBusy(ctx, c, from, to)
	if err != nil {
		return 0, err
	}
	if token != "" {
		c.SyncToken = token
	}
	// Replace prior import blocks for this provider on this resource.
	src := "external:" + c.Provider
	_ = s.Busy.DeleteBySourcePrefix(ctx, sc.TenantID, sc.PartitionID, c.ResourceID, src)
	n := 0
	for _, b := range blocks {
		row := &models.BusyBlock{
			ResourceID: c.ResourceID, StartAt: b.Start.UTC(), EndAt: b.End.UTC(),
			Source: src, Note: b.Note, ExternalKey: b.ExternalKey,
		}
		row.TenantID = sc.TenantID
		row.PartitionID = sc.PartitionID
		if err := s.Busy.UpsertExternal(ctx, row); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// DrainExportOutbox pushes pending export actions. Returns (sent, errors).
func (s *Service) DrainExportOutbox(ctx context.Context, limit int) (int, int) {
	if s.SyncOutbox == nil {
		return 0, 0
	}
	rows, err := s.SyncOutbox.ListPending(ctx, limit)
	if err != nil {
		util.Log(ctx).WithError(err).Warn("calendar: list sync outbox")
		return 0, 1
	}
	sent, errs := 0, 0
	for _, row := range rows {
		if err := s.deliverExport(ctx, row); err != nil {
			errs++
			_ = s.SyncOutbox.MarkFailed(ctx, row.ID, row.Attempts+1, err.Error())
			continue
		}
		_ = s.SyncOutbox.MarkSent(ctx, row.ID)
		sent++
	}
	return sent, errs
}

func (s *Service) deliverExport(ctx context.Context, row *models.SyncOutbox) error {
	conn, err := s.Connections.GetInPartition(ctx, row.TenantID, row.PartitionID, row.ConnectionID)
	if err != nil {
		return err
	}
	if conn == nil || !conn.ExportBookings {
		return nil
	}
	p := s.Providers.Get(conn.Provider)
	if p == nil || !p.Ready() {
		return fmt.Errorf("calendar: provider %s not ready for export", conn.Provider)
	}
	b, err := s.Bookings.GetInPartition(ctx, row.TenantID, row.PartitionID, row.BookingID)
	if err != nil {
		return err
	}
	if b == nil {
		return nil
	}
	lines, err := s.Lines.ListByBooking(ctx, b.ID)
	if err != nil {
		return err
	}
	var line *models.BookingLine
	for _, ln := range lines {
		if ln.ResourceID == conn.ResourceID {
			line = ln
			break
		}
	}
	if row.Action == models.ActionDelete {
		if line == nil || line.ExternalEventID == "" {
			return nil
		}
		return p.DeleteExport(ctx, conn, line.ExternalEventID)
	}
	ev := models.BookingToExternalEvent(b, line)
	extID, err := p.ExportBooking(ctx, conn, ev)
	if err != nil {
		return err
	}
	if line != nil && extID != "" {
		line.ExternalEventID = extID
		_, _ = s.Lines.Update(ctx, line, "external_event_id", "modified_at", "modified_by")
	}
	return nil
}

func (s *Service) enqueueExports(ctx context.Context, sc Scope, b *models.Booking, lines []*models.BookingLine) {
	if s.SyncOutbox == nil || s.Connections == nil {
		return
	}
	for _, ln := range lines {
		conns, err := s.Connections.ListByResource(ctx, sc.TenantID, sc.PartitionID, ln.ResourceID)
		if err != nil {
			continue
		}
		for _, c := range conns {
			if !c.ExportBookings || c.Status == models.ConnDisabled {
				continue
			}
			_ = s.SyncOutbox.Create(ctx, &models.SyncOutbox{
				ConnectionID: c.ID, BookingID: b.ID, Action: models.ActionUpsert,
				Status: models.OutboxPending,
			})
		}
	}
}

func (s *Service) enqueueDeletes(ctx context.Context, sc Scope, b *models.Booking, lines []*models.BookingLine) {
	if s.SyncOutbox == nil || s.Connections == nil {
		return
	}
	for _, ln := range lines {
		if ln.ExternalEventID == "" {
			continue
		}
		conns, err := s.Connections.ListByResource(ctx, sc.TenantID, sc.PartitionID, ln.ResourceID)
		if err != nil {
			continue
		}
		for _, c := range conns {
			if !c.ExportBookings {
				continue
			}
			_ = s.SyncOutbox.Create(ctx, &models.SyncOutbox{
				ConnectionID: c.ID, BookingID: b.ID, Action: models.ActionDelete,
				Status: models.OutboxPending,
			})
		}
	}
}

func (s *Service) buildDemands(ctx context.Context, sc Scope, resourceIDs []string, qty map[string]int) ([]models.ResourceDemand, error) {
	demands := make([]models.ResourceDemand, 0, len(resourceIDs))
	seen := map[string]struct{}{}
	for _, id := range resourceIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		res, err := s.Resources.GetInPartition(ctx, sc.TenantID, sc.PartitionID, id)
		if err != nil {
			return nil, err
		}
		if res == nil || res.Status != models.ResourceActive {
			return nil, fmt.Errorf("%w: resource %s", models.ErrNotFound, id)
		}
		av, err := s.Availability.GetByResource(ctx, sc.TenantID, sc.PartitionID, id)
		if err != nil {
			return nil, err
		}
		if av == nil || av.RulesJSON == "" || av.RulesJSON == "[]" {
			return nil, fmt.Errorf("%w: empty availability for resource %s", models.ErrInvalid, id)
		}
		var rules []models.WeekRule
		_ = json.Unmarshal([]byte(av.RulesJSON), &rules)
		var ex []models.ExceptionDay
		_ = json.Unmarshal([]byte(av.ExceptionsJSON), &ex)
		q := 1
		if qty != nil && qty[id] > 0 {
			q = qty[id]
		}
		tz := av.Timezone
		if tz == "" {
			tz = res.Timezone
		}
		demands = append(demands, models.ResourceDemand{
			ResourceID: id, Capacity: res.Capacity, Quantity: q,
			Rules: rules, Exceptions: ex, Timezone: tz,
		})
	}
	return demands, nil
}

func (s *Service) busyMap(ctx context.Context, sc Scope, resourceIDs []string, from, to time.Time) (map[string][]models.BusyInterval, error) {
	return s.busyMapExcludingBooking(ctx, sc, resourceIDs, from, to, "")
}

func (s *Service) busyMapExcludingBooking(ctx context.Context, sc Scope, resourceIDs []string, from, to time.Time, excludeBookingID string) (map[string][]models.BusyInterval, error) {
	out := map[string][]models.BusyInterval{}
	blocks, err := s.Busy.ListInRange(ctx, sc.TenantID, sc.PartitionID, resourceIDs, from, to)
	if err != nil {
		return nil, err
	}
	for _, b := range blocks {
		out[b.ResourceID] = append(out[b.ResourceID], models.BusyInterval{
			ResourceID: b.ResourceID, Start: b.StartAt, End: b.EndAt, Source: b.Source, Note: b.Note,
		})
	}
	bookings, err := s.Bookings.ListOverlapping(ctx, sc.TenantID, sc.PartitionID, resourceIDs, from, to)
	if err != nil {
		return nil, err
	}
	if len(bookings) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(bookings))
	byID := map[string]*models.Booking{}
	for _, b := range bookings {
		if excludeBookingID != "" && b.ID == excludeBookingID {
			continue
		}
		ids = append(ids, b.ID)
		byID[b.ID] = b
	}
	lines, err := s.Lines.ListByBookings(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, ln := range lines {
		b := byID[ln.BookingID]
		if b == nil {
			continue
		}
		// Quantity expands to N unit busy intervals for capacity math.
		q := ln.Quantity
		if q <= 0 {
			q = 1
		}
		for i := 0; i < q; i++ {
			out[ln.ResourceID] = append(out[ln.ResourceID], models.BusyInterval{
				ResourceID: ln.ResourceID, Start: b.StartAt, End: b.EndAt, Source: "booking",
			})
		}
	}
	return out, nil
}
