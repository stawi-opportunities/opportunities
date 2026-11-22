package setupmode

import (
	"testing"

	"github.com/pitabwire/frame/v2/config"
)

func TestShouldMigrate(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		cfg  config.ConfigurationDefault
		want bool
	}{
		{name: "nothing set", want: false},
		{name: "frame DO_SETUP", cfg: config.ConfigurationDefault{DoSetup: true}, want: true},
		{name: "frame DO_MIGRATION", cfg: config.ConfigurationDefault{DatabaseMigrate: true}, want: true},
		{name: "legacy DO_DATABASE_MIGRATE=true", env: map[string]string{LegacyMigrateEnv: "true"}, want: true},
		{name: "legacy DO_DATABASE_MIGRATE=1", env: map[string]string{LegacyMigrateEnv: "1"}, want: true},
		{name: "legacy DO_DATABASE_MIGRATE=false", env: map[string]string{LegacyMigrateEnv: "false"}, want: false},
		{name: "legacy garbage is not migrate", env: map[string]string{LegacyMigrateEnv: "yes please"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(LegacyMigrateEnv, "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg := tc.cfg
			if got := ShouldMigrate(&cfg); got != tc.want {
				t.Fatalf("ShouldMigrate() = %v, want %v", got, tc.want)
			}
		})
	}
}
