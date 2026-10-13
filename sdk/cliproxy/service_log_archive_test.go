package cliproxy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logarchive"
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

func TestSDKRestartDisablesPreviousArchiveState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WRITABLE_PATH", dir)
	// Stop startup after logging is configured, without opening a listener or
	// requiring authentication providers. Run still performs normal shutdown.
	authFile := filepath.Join(dir, "not-a-directory")
	if errWrite := os.WriteFile(authFile, []byte("test"), 0600); errWrite != nil {
		t.Fatal(errWrite)
	}
	t.Cleanup(func() {
		if errConfigure := logarchive.Configure(dir, config.LogArchiveConfig{}); errConfigure != nil {
			t.Error(errConfigure)
		}
	})
	archived := &Service{cfg: &config.Config{AuthDir: authFile, LogArchive: config.LogArchiveConfig{
		Enabled: true, Bucket: "archive-test", Region: "us-east-1",
		AccessKeyID: "test", SecretAccessKey: "test",
	}}}
	if errRun := archived.Run(context.Background()); errRun == nil || !strings.Contains(errRun.Error(), "not a directory") {
		t.Fatalf("expected startup to stop after log configuration, got %v", errRun)
	}
	if !logarchive.Enabled() {
		t.Fatal("shutdown must preserve the previous service's pending logs")
	}
	unarchived := &Service{cfg: &config.Config{AuthDir: authFile}}
	if errRun := unarchived.Run(context.Background()); errRun == nil || !strings.Contains(errRun.Error(), "not a directory") {
		t.Fatalf("expected replacement startup to stop after log configuration, got %v", errRun)
	}
	if logarchive.Enabled() {
		t.Fatal("replacement SDK service retained stale archival state without an upload worker")
	}
	release, allowed := logarchive.RetentionGuard()
	release()
	if !allowed {
		t.Fatal("replacement SDK service still suppresses ordinary log retention")
	}
}
