package business

import (
	"context"
	"fmt"
	"time"

	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/apps/calendar/service/models"
)

// ExternalProvider syncs free/busy and bookings with third-party calendars.
// Implementations must be safe for concurrent use.
type ExternalProvider interface {
	Name() string
	// Ready reports whether credentials/env allow live calls.
	Ready() bool
	ImportBusy(ctx context.Context, conn *models.ExternalConnection, from, to time.Time) (blocks []ImportedBusy, syncToken string, err error)
	ExportBooking(ctx context.Context, conn *models.ExternalConnection, event models.ExternalEvent) (externalEventID string, err error)
	DeleteExport(ctx context.Context, conn *models.ExternalConnection, externalEventID string) error
}

// ImportedBusy is one free/busy interval from a provider.
type ImportedBusy struct {
	Start       time.Time
	End         time.Time
	ExternalKey string
	Note        string
}

// ProviderRegistry maps provider name → implementation.
type ProviderRegistry map[string]ExternalProvider

func (r ProviderRegistry) Get(name string) ExternalProvider {
	if r == nil {
		return nil
	}
	return r[name]
}

// SyncWorker imports free/busy and exports bookings via provider ports.
type SyncWorker struct {
	Service  *Service
	Interval time.Duration
}

func (w *SyncWorker) Run(ctx context.Context) {
	if w.Service == nil {
		return
	}
	interval := w.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	log := util.Log(ctx)
	log.Info("calendar: sync worker started")
	t := time.NewTicker(interval)
	defer t.Stop()
	// Export outbox drain does not need claims; uses stored tenant on rows.
	w.Service.DrainExportOutbox(ctx, 50)
	for {
		select {
		case <-ctx.Done():
			log.Info("calendar: sync worker stopped")
			return
		case <-t.C:
			w.Service.DrainExportOutbox(ctx, 50)
		}
	}
}

// NoopProvider is a registered stub when live OAuth is not configured.
// Ready() is false so TriggerSync reports provider unavailable rather than silently succeeding.
type NoopProvider struct {
	ProviderName string
}

func (n NoopProvider) Name() string { return n.ProviderName }
func (n NoopProvider) Ready() bool  { return false }

func (n NoopProvider) ImportBusy(context.Context, *models.ExternalConnection, time.Time, time.Time) ([]ImportedBusy, string, error) {
	return nil, "", fmt.Errorf("calendar: provider %s not configured", n.ProviderName)
}

func (n NoopProvider) ExportBooking(context.Context, *models.ExternalConnection, models.ExternalEvent) (string, error) {
	return "", fmt.Errorf("calendar: provider %s not configured", n.ProviderName)
}

func (n NoopProvider) DeleteExport(context.Context, *models.ExternalConnection, string) error {
	return fmt.Errorf("calendar: provider %s not configured", n.ProviderName)
}

// MemoryProvider is an in-process provider for tests and local sync verification.
// It stores exported events and can re-import them as busy.
type MemoryProvider struct {
	Events map[string]models.ExternalEvent // key: external event id
}

func NewMemoryProvider() *MemoryProvider {
	return &MemoryProvider{Events: map[string]models.ExternalEvent{}}
}

func (m *MemoryProvider) Name() string { return "memory" }
func (m *MemoryProvider) Ready() bool  { return true }

func (m *MemoryProvider) ImportBusy(_ context.Context, conn *models.ExternalConnection, from, to time.Time) ([]ImportedBusy, string, error) {
	var out []ImportedBusy
	for id, ev := range m.Events {
		if !ev.End.After(from) || !ev.Start.Before(to) {
			continue
		}
		// Optionally filter by calendar id in metadata later.
		_ = conn
		out = append(out, ImportedBusy{
			Start: ev.Start, End: ev.End,
			ExternalKey: "memory:" + id,
			Note:        ev.Title,
		})
	}
	return out, time.Now().UTC().Format(time.RFC3339), nil
}

func (m *MemoryProvider) ExportBooking(_ context.Context, _ *models.ExternalConnection, event models.ExternalEvent) (string, error) {
	id := event.ExternalEventID
	if id == "" {
		id = "mem_" + event.UID
		if id == "mem_" {
			id = "mem_" + util.IDString()
		}
	}
	event.ExternalEventID = id
	m.Events[id] = event
	return id, nil
}

func (m *MemoryProvider) DeleteExport(_ context.Context, _ *models.ExternalConnection, externalEventID string) error {
	delete(m.Events, externalEventID)
	return nil
}

// ConfiguredHTTPProvider is a skeleton for Google/Microsoft/CalDAV live calls.
// When Endpoint and HTTP client are set, Ready is true; actual HTTP mapping is
// delegated so credentials/env can be completed without changing API surface.
type ConfiguredHTTPProvider struct {
	ProviderName string
	// Enabled set true when env credentials present.
	Enabled bool
	// ExportFn / ImportFn optional hooks (inject real Google Graph/CalDAV adapters).
	ImportFn func(ctx context.Context, conn *models.ExternalConnection, from, to time.Time) ([]ImportedBusy, string, error)
	ExportFn func(ctx context.Context, conn *models.ExternalConnection, event models.ExternalEvent) (string, error)
	DeleteFn func(ctx context.Context, conn *models.ExternalConnection, externalEventID string) error
}

func (p ConfiguredHTTPProvider) Name() string { return p.ProviderName }
func (p ConfiguredHTTPProvider) Ready() bool  { return p.Enabled }

func (p ConfiguredHTTPProvider) ImportBusy(ctx context.Context, conn *models.ExternalConnection, from, to time.Time) ([]ImportedBusy, string, error) {
	if p.ImportFn == nil {
		return nil, "", fmt.Errorf("calendar: %s import not wired", p.ProviderName)
	}
	return p.ImportFn(ctx, conn, from, to)
}

func (p ConfiguredHTTPProvider) ExportBooking(ctx context.Context, conn *models.ExternalConnection, event models.ExternalEvent) (string, error) {
	if p.ExportFn == nil {
		return "", fmt.Errorf("calendar: %s export not wired", p.ProviderName)
	}
	return p.ExportFn(ctx, conn, event)
}

func (p ConfiguredHTTPProvider) DeleteExport(ctx context.Context, conn *models.ExternalConnection, externalEventID string) error {
	if p.DeleteFn == nil {
		return fmt.Errorf("calendar: %s delete not wired", p.ProviderName)
	}
	return p.DeleteFn(ctx, conn, externalEventID)
}
