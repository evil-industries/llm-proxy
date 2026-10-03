package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionAffinityDefaultAndExplicitOptOutRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       bool
	}{
		{"omitted", "port: 8317\n", true},
		{"enabled", "routing:\n  session-affinity: true\n", true},
		{"disabled", "routing:\n  session-affinity: false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Routing.SessionAffinity != tc.want {
				t.Fatalf("affinity = %v", cfg.Routing.SessionAffinity)
			}
			if err := SaveConfigPreserveComments(path, cfg); err != nil {
				t.Fatal(err)
			}
			reloaded, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Routing.SessionAffinity != tc.want {
				t.Fatal("save changed affinity")
			}
		})
	}
}
