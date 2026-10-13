package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogArchiveConfigLoadAndParse(t *testing.T) {
	payload := []byte("log-archive:\n  enabled: true\n  bucket: permanent-logs\n  access-key-id: secret-id\n  secret-access-key: secret-key\n  session-token: secret-token\n")
	filename := filepath.Join(t.TempDir(), "config.yaml")
	if errWrite := os.WriteFile(filename, payload, 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	parsed, errParse := ParseConfigBytes(payload)
	if errParse != nil {
		t.Fatal(errParse)
	}
	loaded, errLoad := LoadConfigOptional(filename, false)
	if errLoad != nil {
		t.Fatal(errLoad)
	}
	for _, cfg := range []*Config{parsed, loaded} {
		if cfg.LogArchive.Endpoint != "https://s3.amazonaws.com" || cfg.LogArchive.Prefix != "logs" || cfg.LogArchive.SecretAccessKey != "secret-key" {
			t.Fatalf("archive defaults or credentials were not preserved")
		}
		encoded, errJSON := json.Marshal(cfg)
		if errJSON != nil || strings.Contains(string(encoded), "secret-id") || strings.Contains(string(encoded), "secret-key") || strings.Contains(string(encoded), "secret-token") {
			t.Fatal("config JSON exposed archive credentials")
		}
	}
}

func TestLogArchiveConfigValidation(t *testing.T) {
	for _, fragment := range []string{
		"bucket: ''", "bucket: invalid/bucket", "endpoint: https://user:password@host", "endpoint: ftp://host",
		"endpoint: https://host/path", "endpoint: https://host?secret=value", "endpoint: https://host#fragment",
		"access-key-id: incomplete", "session-token: incomplete", "prefix: ../logs", "prefix: logs//nested",
	} {
		t.Run(fragment, func(t *testing.T) {
			payload := "log-archive:\n  enabled: true\n"
			if !strings.HasPrefix(fragment, "bucket:") {
				payload += "  bucket: permanent-logs\n"
			}
			payload += "  " + fragment + "\n"
			if _, errParse := ParseConfigBytes([]byte(payload)); errParse == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	if _, errParse := ParseConfigBytes([]byte("log-archive:\n  enabled: true\n  bucket: permanent-logs\n  endpoint: http://localhost:9000\n")); errParse != nil {
		t.Fatal(errParse)
	}
}
