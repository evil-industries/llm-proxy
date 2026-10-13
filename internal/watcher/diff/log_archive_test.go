package diff

import (
	"reflect"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestLogArchiveChangesDoNotDiscloseCredentials(t *testing.T) {
	before := &config.Config{}
	after := &config.Config{LogArchive: config.LogArchiveConfig{
		Enabled: true, Endpoint: "https://private-storage.example.com",
		Bucket: "private-logs", AccessKeyID: "private-access-key",
		SecretAccessKey: "private-secret", SessionToken: "private-token",
	}}
	if got := BuildConfigChangeDetails(before, after); !reflect.DeepEqual(got, []string{"log-archive: configuration changed"}) {
		t.Fatalf("archive config diff must be redacted, got %v", got)
	}
	rotated := *after
	rotated.LogArchive.SecretAccessKey = "rotated-secret"
	if got := BuildConfigChangeDetails(after, &rotated); !reflect.DeepEqual(got, []string{"log-archive: configuration changed"}) {
		t.Fatalf("credential-only rotations must be reported without values, got %v", got)
	}
}
