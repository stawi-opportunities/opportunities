package applications_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestNewR2BlobStore_RejectsEmptyConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  applications.R2BlobConfig
	}{
		{"missing account", applications.R2BlobConfig{AccessKeyID: "k", SecretAccessKey: "s", Bucket: "b"}},
		{"missing access key", applications.R2BlobConfig{AccountID: "a", SecretAccessKey: "s", Bucket: "b"}},
		{"missing secret", applications.R2BlobConfig{AccountID: "a", AccessKeyID: "k", Bucket: "b"}},
		{"missing bucket", applications.R2BlobConfig{AccountID: "a", AccessKeyID: "k", SecretAccessKey: "s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applications.NewR2BlobStore(tc.cfg)
			require.Error(t, err)
		})
	}
}

func TestNewR2BlobStore_AcceptsFullConfig(t *testing.T) {
	r, err := applications.NewR2BlobStore(applications.R2BlobConfig{
		AccountID: "acct", AccessKeyID: "key", SecretAccessKey: "secret", Bucket: "bkt",
	})
	require.NoError(t, err)
	require.NotNil(t, r)
}
