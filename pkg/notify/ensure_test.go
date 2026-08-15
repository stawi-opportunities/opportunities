package notify_test

import (
	"context"
	"errors"
	"io"
	"testing"

	notificationv1 "buf.build/gen/go/antinvestor/notification/protocolbuffers/go/notification/v1"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/notify"
)

func TestRequiredTemplateNames_Complete(t *testing.T) {
	t.Parallel()
	names := notify.RequiredTemplateNames()
	require.Len(t, names, 5)
	want := map[string]bool{
		notify.DefaultTemplateMatchesReady:     true,
		notify.DefaultTemplateMatchesDigest:    true,
		notify.DefaultTemplateWeeklyJobsDigest: true,
		notify.DefaultTemplateCVStaleNudge:     true,
		notify.DefaultTemplateATSReport:        true,
	}
	for _, n := range names {
		require.True(t, want[n], "unexpected or missing default: %s", n)
		delete(want, n)
	}
	require.Empty(t, want)
}

func TestCatalog_EnvOverrideNames(t *testing.T) {
	t.Parallel()
	cfg := notify.Templates{
		MatchesReady:  "template.custom.ready",
		MatchesDigest: "template.custom.digest",
	}
	cat := notify.Catalog(cfg)
	var foundReady, foundDigest bool
	for _, d := range cat {
		require.NotEmpty(t, d.Data["subject"], d.Name)
		require.NotEmpty(t, d.Data["html"], d.Name)
		require.NotEmpty(t, d.Data["text"], d.Name)
		if d.Name == "template.custom.ready" {
			foundReady = true
		}
		if d.Name == "template.custom.digest" {
			foundDigest = true
		}
	}
	require.True(t, foundReady)
	require.True(t, foundDigest)
}

// ensureAdmin forces TemplateSearch to fail so EnsureAll falls through to TemplateSave.
type ensureAdmin struct {
	saved []string
}

func (a *ensureAdmin) TemplateSearch(
	context.Context,
	*connect.Request[notificationv1.TemplateSearchRequest],
) (*connect.ServerStreamForClient[notificationv1.TemplateSearchResponse], error) {
	return nil, io.EOF
}

func (a *ensureAdmin) TemplateSave(
	_ context.Context,
	req *connect.Request[notificationv1.TemplateSaveRequest],
) (*connect.Response[notificationv1.TemplateSaveResponse], error) {
	name := req.Msg.GetName()
	if name == "" {
		return nil, errors.New("empty name")
	}
	if req.Msg.GetData() == nil || len(req.Msg.GetData().GetFields()) == 0 {
		return nil, errors.New("empty data")
	}
	a.saved = append(a.saved, name)
	return connect.NewResponse(&notificationv1.TemplateSaveResponse{
		Data: &notificationv1.Template{Name: name},
	}), nil
}

func TestEnsureAll_SavesMissingTemplates(t *testing.T) {
	t.Parallel()
	admin := &ensureAdmin{}
	err := notify.EnsureAll(context.Background(), admin, notify.Templates{})
	require.NoError(t, err)
	require.Len(t, admin.saved, 5)
}

func TestEnsureAll_NilClient(t *testing.T) {
	t.Parallel()
	err := notify.EnsureAll(context.Background(), nil, notify.Templates{})
	require.Error(t, err)
}

type failSaveAdmin struct{}

func (f *failSaveAdmin) TemplateSearch(
	context.Context,
	*connect.Request[notificationv1.TemplateSearchRequest],
) (*connect.ServerStreamForClient[notificationv1.TemplateSearchResponse], error) {
	return nil, errors.New("no search")
}

func (f *failSaveAdmin) TemplateSave(
	context.Context,
	*connect.Request[notificationv1.TemplateSaveRequest],
) (*connect.Response[notificationv1.TemplateSaveResponse], error) {
	return nil, errors.New("save denied")
}

func TestEnsureAll_SaveError(t *testing.T) {
	t.Parallel()
	err := notify.EnsureAll(context.Background(), &failSaveAdmin{}, notify.Templates{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to ensure")
}
