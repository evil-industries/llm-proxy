package cliproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/api/handlers/management"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/watcher"
)

func TestManagementKeyRemovalSurvivesPendingWatcherReload(t *testing.T) {
	cfg := &config.Config{}
	cfg.APIKeys = []string{"anchor", "revoke"}
	cfg.AuthDir = t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if errWrite := os.WriteFile(configPath, []byte("api-keys: [anchor, revoke]\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	if errSave := config.SaveConfigPreserveComments(configPath, cfg); errSave != nil {
		t.Fatal(errSave)
	}
	h := management.NewHandler(cfg, configPath, nil)
	service := &Service{cfg: cfg}
	// Keep the real service commit ordering and runtime serialization, replacing
	// only the server call that installs the management handler's config.
	service.updateServerClientsContextFn = func(_ context.Context, cfg *config.Config) bool {
		h.SetConfig(cfg)
		return true
	}
	entered := make(chan struct{})
	saved := make(chan struct{})
	olderReloadDone := make(chan struct{})
	var callbacks atomic.Int32
	w, errWatcher := watcher.NewWatcher(configPath, cfg.AuthDir, func(cfg *config.Config) {
		if callbacks.Add(1) == 1 {
			close(entered)
			<-saved
		}
		service.applyWatcherConfigUpdate(cfg)
	})
	if errWatcher != nil {
		t.Fatal(errWatcher)
	}
	t.Cleanup(func() {
		if errStop := w.Stop(); errStop != nil {
			t.Error(errStop)
		}
	})
	w.SetConfig(cfg.CloneForRuntime())
	service.watcher = &WatcherWrapper{reloadConfigIfChanged: w.ReloadConfigIfChanged}
	var saves atomic.Int32
	h.SetConfigReloadHook(func(context.Context, *config.Config) {
		if saves.Add(1) == 1 {
			// The mutation is on disk before the older callback finishes. The
			// older reload must not record this new disk content as applied.
			close(saved)
			<-olderReloadDone
		}
		service.reloadConfigFromWatcher()
	})
	mutate := func(action, value string) {
		t.Helper()
		body, errMarshal := json.Marshal(map[string]string{"action": action, "value": value})
		if errMarshal != nil {
			t.Fatal(errMarshal)
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/api-keys/mutate", strings.NewReader(string(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		h.MutateAPIKey(c)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, recorder.Code, recorder.Body.String())
		}
	}
	go func() {
		w.ReloadConfigIfChanged()
		close(olderReloadDone)
	}()
	<-entered
	mutate("remove", "revoke")
	if !slices.Equal(service.cfg.APIKeys, []string{"anchor"}) {
		t.Fatalf("runtime keys after removal = %v", service.cfg.APIKeys)
	}
	mutate("add", "new")
	loaded, errLoad := config.LoadConfig(configPath)
	if errLoad != nil {
		t.Fatal(errLoad)
	}
	if !slices.Equal(loaded.APIKeys, []string{"anchor", "new"}) {
		t.Fatalf("persisted keys = %v", loaded.APIKeys)
	}
}
