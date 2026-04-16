package services

import (
	"context"
	"fmt"

	"buf.build/gen/go/antinvestor/files/connectrpc/go/files/v1/filesv1connect"
	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	"buf.build/gen/go/antinvestor/payment/connectrpc/go/v1/paymentv1connect"
	"buf.build/gen/go/antinvestor/profile/connectrpc/go/profile/v1/profilev1connect"
	apis "github.com/antinvestor/common"
	"github.com/antinvestor/common/connection"
	"github.com/antinvestor/service-files/apps/redirect/gen/redirect/v1/redirectv1connect"
	"github.com/pitabwire/util"
)

// Clients holds optional Connect RPC clients for antinvestor platform services.
// Each field may be nil when the corresponding service URI is not configured.
type Clients struct {
	Notification notificationv1connect.NotificationServiceClient
	Files        filesv1connect.FilesServiceClient
	Redirect     redirectv1connect.RedirectServiceClient
	Payment      paymentv1connect.PaymentServiceClient
	Profile      profilev1connect.ProfileServiceClient
}

// ClientConfig holds the URIs for each service.
type ClientConfig struct {
	NotificationURI string
	FileURI         string
	RedirectURI     string
	BillingURI      string
	ProfileURI      string
}

// NewClients creates Connect RPC clients for each configured service.
// Missing URIs are silently skipped — the corresponding client field stays nil.
func NewClients(ctx context.Context, cfg any, cc ClientConfig) (*Clients, error) {
	log := util.Log(ctx)
	clients := &Clients{}
	var firstErr error

	record := func(name string, err error) {
		log.WithError(err).WithField("service", name).Warn("service client init failed")
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", name, err)
		}
	}

	if cc.NotificationURI != "" {
		cli, err := connection.NewServiceClient(ctx, cfg, apis.ServiceTarget{
			Endpoint:  cc.NotificationURI,
			Audiences: []string{"service_notification"},
		}, notificationv1connect.NewNotificationServiceClient)
		if err != nil {
			record("notification", err)
		} else {
			clients.Notification = cli
		}
	}

	if cc.FileURI != "" {
		cli, err := connection.NewServiceClient(ctx, cfg, apis.ServiceTarget{
			Endpoint:  cc.FileURI,
			Audiences: []string{"service_files"},
		}, filesv1connect.NewFilesServiceClient)
		if err != nil {
			record("files", err)
		} else {
			clients.Files = cli
		}
	}

	if cc.RedirectURI != "" {
		cli, err := connection.NewServiceClient(ctx, cfg, apis.ServiceTarget{
			Endpoint:  cc.RedirectURI,
			Audiences: []string{"service_files"},
		}, redirectv1connect.NewRedirectServiceClient)
		if err != nil {
			record("redirect", err)
		} else {
			clients.Redirect = cli
		}
	}

	if cc.BillingURI != "" {
		cli, err := connection.NewServiceClient(ctx, cfg, apis.ServiceTarget{
			Endpoint:  cc.BillingURI,
			Audiences: []string{"service_payment"},
		}, paymentv1connect.NewPaymentServiceClient)
		if err != nil {
			record("billing", err)
		} else {
			clients.Payment = cli
		}
	}

	if cc.ProfileURI != "" {
		cli, err := connection.NewServiceClient(ctx, cfg, apis.ServiceTarget{
			Endpoint:  cc.ProfileURI,
			Audiences: []string{"service_profile"},
		}, profilev1connect.NewProfileServiceClient)
		if err != nil {
			record("profile", err)
		} else {
			clients.Profile = cli
		}
	}

	return clients, firstErr
}
