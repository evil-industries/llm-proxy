package cliproxy

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestHomeOverlayPreservesNodeLogArchive(t *testing.T) {
	archive := config.LogArchiveConfig{Enabled: true, Bucket: "node-logs", SecretAccessKey: "node-secret", AccessKeyID: "node-access"}
	service := &Service{cfg: &config.Config{LogArchive: archive}}
	remote := &config.Config{LoggingToFile: true}
	work, errStage := service.stageHomeOverlayWithClient(context.Background(), remote, nil)
	if errStage != nil {
		t.Fatal(errStage)
	}
	if work.config == nil || work.config.LogArchive != archive {
		t.Fatal("Home overlay disabled or replaced node log archival")
	}
	if !work.config.LoggingToFile || remote.LogArchive.Enabled {
		t.Fatal("Home capture setting was lost or input was mutated")
	}
}
