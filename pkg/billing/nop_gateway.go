package billing

import (
	"context"
	"errors"
)

// ErrGatewayUnavailable is returned by NopGateway so callers can map an
// unconfigured payment backend to a clear 503 rather than a panic.
var ErrGatewayUnavailable = errors.New("billing: payment gateway not configured")

// NopGateway is the Gateway used when BILLING_SERVICE_URI is unset. It
// keeps the plans route + dashboard working in degraded mode while failing
// checkout creation explicitly. The status poll reports pending so a UI
// that already started a checkout against a configured deploy doesn't see
// a hard error mid-flow.
type NopGateway struct{}

func (NopGateway) CreateCheckout(_ context.Context, _ CheckoutRequest) (CheckoutResult, error) {
	return CheckoutResult{}, ErrGatewayUnavailable
}

func (NopGateway) CheckoutStatus(_ context.Context, _ string) (StatusResult, error) {
	return StatusResult{Status: StatusPending}, nil
}
