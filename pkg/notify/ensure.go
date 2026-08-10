package notify

import (
	"context"
	"fmt"
	"strings"

	"buf.build/gen/go/antinvestor/notification/connectrpc/go/notification/v1/notificationv1connect"
	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"connectrpc.com/connect"
	"github.com/pitabwire/util"
	"google.golang.org/protobuf/types/known/structpb"
)

// TemplateAdmin is the subset of NotificationServiceClient used to ensure templates.
// *notificationv1connect client satisfies this in production.
type TemplateAdmin interface {
	TemplateSearch(context.Context, *connect.Request[notificationv1.TemplateSearchRequest]) (*connect.ServerStreamForClient[notificationv1.TemplateSearchResponse], error)
	TemplateSave(context.Context, *connect.Request[notificationv1.TemplateSaveRequest]) (*connect.Response[notificationv1.TemplateSaveResponse], error)
}

// EnsureAll creates any missing opportunities notification templates in
// service-notification. Idempotent: existing names are skipped.
// When cli is nil, returns an error so setup can fail loudly.
func EnsureAll(ctx context.Context, cli TemplateAdmin, cfg Templates) error {
	if cli == nil {
		return fmt.Errorf("notify: ensure templates: notification client is nil")
	}
	log := util.Log(ctx)
	defs := Catalog(cfg)
	var failed []string
	for _, def := range defs {
		if err := ensureOne(ctx, cli, def); err != nil {
			log.WithError(err).WithField("template", def.Name).Error("notify: ensure template failed")
			failed = append(failed, def.Name)
			continue
		}
		log.WithField("template", def.Name).Info("notify: template ensured")
	}
	if len(failed) > 0 {
		return fmt.Errorf("notify: failed to ensure %d template(s): %s", len(failed), strings.Join(failed, ", "))
	}
	return nil
}

func ensureOne(ctx context.Context, cli TemplateAdmin, def Definition) error {
	name := strings.TrimSpace(def.Name)
	if name == "" {
		return fmt.Errorf("empty template name")
	}
	lang := strings.TrimSpace(def.LanguageCode)
	if lang == "" {
		lang = "en"
	}

	exists, err := templateExists(ctx, cli, name)
	if err != nil {
		// Search may be permission-denied on some environments; attempt save anyway.
		util.Log(ctx).WithError(err).WithField("template", name).
			Warn("notify: template search failed; attempting save")
	} else if exists {
		return nil
	}

	dataMap := make(map[string]any, len(def.Data))
	for k, v := range def.Data {
		dataMap[k] = v
	}
	data, err := structpb.NewStruct(dataMap)
	if err != nil {
		return fmt.Errorf("template %s payload: %w", name, err)
	}
	extra, err := structpb.NewStruct(map[string]any{
		"description": def.Description,
		"product":     "opportunities",
	})
	if err != nil {
		return fmt.Errorf("template %s extra: %w", name, err)
	}

	_, err = cli.TemplateSave(ctx, connect.NewRequest(&notificationv1.TemplateSaveRequest{
		Name:         name,
		LanguageCode: lang,
		Data:         data,
		Extra:        extra,
	}))
	if err != nil {
		// Race: another replica created it; treat as success if it now exists.
		if exists2, e2 := templateExists(ctx, cli, name); e2 == nil && exists2 {
			return nil
		}
		return fmt.Errorf("TemplateSave %s: %w", name, err)
	}
	return nil
}

func templateExists(ctx context.Context, cli TemplateAdmin, name string) (bool, error) {
	stream, err := cli.TemplateSearch(ctx, connect.NewRequest(&notificationv1.TemplateSearchRequest{
		Query:        name,
		LanguageCode: "en",
		Count:        50,
		Page:         0,
	}))
	if err != nil {
		return false, err
	}
	defer func() { _ = stream.Close() }()

	for stream.Receive() {
		msg := stream.Msg()
		if msg == nil {
			continue
		}
		for _, t := range msg.GetData() {
			if strings.EqualFold(strings.TrimSpace(t.GetName()), name) {
				return true, nil
			}
		}
	}
	if err := stream.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// EnsureFromConfig is a convenience wrapper used by matching setup/migrate.
func EnsureFromConfig(
	ctx context.Context,
	cli notificationv1connect.NotificationServiceClient,
	cfg Templates,
) error {
	return EnsureAll(ctx, cli, cfg)
}
