package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fork-specific sections must load from their v8 paths and their legacy spellings.
func TestForkSectionsUseV8Layout(t *testing.T) {
	v8 := `config-version: 8
observability:
  logs:
    archive:
      enabled: true
      bucket: "logs-bucket"
      region: "eu-central-1"
  notifications:
    enabled: true
    url: "https://ntfy.example.com"
    topic: "quota"
`
	legacy := `log-archive:
  enabled: true
  bucket: "logs-bucket"
  region: "eu-central-1"
notifications:
  enabled: true
  url: "https://ntfy.example.com"
  topic: "quota"
`
	for name, raw := range map[string]string{"v8": v8, "legacy": legacy} {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseConfigBytes([]byte(raw))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !cfg.LogArchive.Enabled || cfg.LogArchive.Bucket != "logs-bucket" {
				t.Fatalf("log archive not loaded: %+v", cfg.LogArchive)
			}
			if !cfg.Notifications.Enabled || cfg.Notifications.Topic != "quota" {
				t.Fatalf("notifications not loaded: %+v", cfg.Notifications)
			}
		})
	}
	if err := ValidateV8Config([]byte(v8)); err != nil {
		t.Fatalf("v8 validation rejected fork sections: %v", err)
	}
}

// A v8 migration must move fork sections instead of commenting them out.
func TestForkSectionsSurviveV8Migration(t *testing.T) {
	raw := "port: 8317\nlog-archive:\n  enabled: false\n  prefix: \"logs\"\nnotifications:\n  enabled: false\n  topic: \"quota\"\n"
	migrated, _, err := NormalizeConfigLayout([]byte(raw), true)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	text := string(migrated)
	if strings.Contains(text, "# log-archive") || strings.Contains(text, "# notifications") {
		t.Fatalf("fork sections were commented out:\n%s", text)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err = os.WriteFile(path, migrated, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load migrated: %v", err)
	}
	if cfg.LogArchive.Prefix != "logs" || cfg.Notifications.Topic != "quota" {
		t.Fatalf("fork sections lost in migration: archive=%+v notifications=%+v", cfg.LogArchive, cfg.Notifications)
	}
}
