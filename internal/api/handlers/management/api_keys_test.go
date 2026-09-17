package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func mutateAPIKeyForTest(h *Handler, action, value string) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(map[string]string{"action": action, "value": value})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/api-keys/mutate", strings.NewReader(string(payload)))
	c.Request.Header.Set("Content-Type", "application/json")
	h.MutateAPIKey(c)
	return rec
}

func apiKeyTestHandler(t *testing.T, keys []string) *Handler {
	t.Helper()
	cfg := &config.Config{}
	cfg.APIKeys = slices.Clone(keys)
	return &Handler{cfg: cfg, configFilePath: writeTestConfigFile(t)}
}

func TestMutateAPIKeyPreservesOtherKeys(t *testing.T) {
	h := apiKeyTestHandler(t, []string{"first", "second", "third"})
	for _, mutation := range []struct{ action, key string }{{"remove", "first"}, {"remove", "second"}, {"add", " fourth "}} {
		rec := mutateAPIKeyForTest(h, mutation.action, mutation.key)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", mutation.action, rec.Code)
		}
	}
	if !slices.Equal(h.cfg.APIKeys, []string{"third", "fourth"}) {
		t.Fatalf("keys = %v", h.cfg.APIKeys)
	}
	loaded, errLoad := config.LoadConfig(h.configFilePath)
	if errLoad != nil {
		t.Fatal(errLoad)
	}
	if !slices.Equal(loaded.APIKeys, h.cfg.APIKeys) {
		t.Fatalf("persisted keys differ: %v", loaded.APIKeys)
	}
}

func TestMutateAPIKeyRejectsRemovingFinalEffectiveKey(t *testing.T) {
	for _, keys := range [][]string{{"only"}, {"only", " only ", "", " "}} {
		h := apiKeyTestHandler(t, keys)
		rec := mutateAPIKeyForTest(h, "remove", "only")
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want conflict", rec.Code)
		}
		if !slices.Equal(h.cfg.APIKeys, keys) {
			t.Fatal("rejected removal changed keys")
		}
	}
}

func TestMutateAPIKeyConcurrentRemovalsKeepAuthentication(t *testing.T) {
	h := apiKeyTestHandler(t, []string{"first", "second"})
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, key := range []string{"first", "second"} {
		go func() { <-start; results <- mutateAPIKeyForTest(h, "remove", key).Code }()
	}
	close(start)
	statuses := []int{<-results, <-results}
	slices.Sort(statuses)
	if !slices.Equal(statuses, []int{http.StatusOK, http.StatusConflict}) {
		t.Fatalf("statuses = %v", statuses)
	}
	if len(h.cfg.APIKeys) != 1 {
		t.Fatalf("remaining keys = %v", h.cfg.APIKeys)
	}
}

func TestMutateAPIKeyConcurrentAdditionsDoNotLoseUpdates(t *testing.T) {
	h := apiKeyTestHandler(t, []string{"anchor"})
	h.SetConfigReloadHook(func(_ context.Context, cfg *config.Config) { h.SetConfig(cfg) })
	const additions = 12
	start := make(chan struct{})
	results := make(chan int, additions)
	var workers sync.WaitGroup
	for i := range additions {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- mutateAPIKeyForTest(h, "add", fmt.Sprintf("key-%d", i)).Code
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for status := range results {
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
	}
	if len(h.cfg.APIKeys) != additions+1 {
		t.Fatalf("got %d keys", len(h.cfg.APIKeys))
	}
	for i := range additions {
		if !slices.Contains(h.cfg.APIKeys, fmt.Sprintf("key-%d", i)) {
			t.Fatalf("missing key %d", i)
		}
	}
}

func TestMutateAPIKeyRejectsInvalidAndStaleChanges(t *testing.T) {
	for _, tc := range []struct {
		action, value string
		status        int
	}{
		{"add", "existing", http.StatusConflict},
		{"remove", "missing", http.StatusConflict},
		{"add", " ", http.StatusBadRequest},
		{"replace", "new", http.StatusBadRequest},
	} {
		h := apiKeyTestHandler(t, []string{"existing"})
		rec := mutateAPIKeyForTest(h, tc.action, tc.value)
		if rec.Code != tc.status {
			t.Fatalf("%s status = %d, want %d", tc.action, rec.Code, tc.status)
		}
		if !slices.Equal(h.cfg.APIKeys, []string{"existing"}) {
			t.Fatal("rejected mutation changed keys")
		}
	}
}

func TestMutateAPIKeyRestoresMemoryAfterPersistenceFailure(t *testing.T) {
	h := apiKeyTestHandler(t, []string{"existing"})
	h.configFilePath = filepath.Join(t.TempDir(), "missing", "config.yaml")
	rec := mutateAPIKeyForTest(h, "add", "new")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if !slices.Equal(h.cfg.APIKeys, []string{"existing"}) {
		t.Fatal("failed persistence changed keys")
	}
}

func TestMutateAPIKeyPreservesPublishedConfigSnapshot(t *testing.T) {
	h := apiKeyTestHandler(t, []string{"anchor", "revoke"})
	published := h.cfg
	if rec := mutateAPIKeyForTest(h, "remove", "revoke"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !slices.Equal(published.APIKeys, []string{"anchor", "revoke"}) {
		t.Fatalf("mutation changed the previously published runtime config: %v", published.APIKeys)
	}
	if !slices.Equal(h.cfg.APIKeys, []string{"anchor"}) {
		t.Fatalf("updated keys = %v", h.cfg.APIKeys)
	}
}

func TestLegacyAPIKeyDeletionCanStillClearList(t *testing.T) {
	h := apiKeyTestHandler(t, []string{"existing"})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v0/management/api-keys?index=0", nil)
	h.DeleteAPIKeys(c)
	if rec.Code != http.StatusOK || len(h.cfg.APIKeys) != 0 {
		t.Fatalf("legacy deletion changed behavior: status=%d keys=%v", rec.Code, h.cfg.APIKeys)
	}
}

func TestMutateAPIKeySkipsPendingOlderReload(t *testing.T) {
	h := apiKeyTestHandler(t, []string{"anchor"})
	h.mu.Lock()
	older := h.reloadSnapshotConfigLocked()
	h.mu.Unlock()
	h.SetConfigReloadHook(func(_ context.Context, cfg *config.Config) { h.SetConfig(cfg) })
	if rec := mutateAPIKeyForTest(h, "add", "first"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// A previously queued async reload must not replace the freshly mutated list.
	h.reloadConfigAfterManagementSave(context.Background(), older)
	if rec := mutateAPIKeyForTest(h, "add", "second"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !slices.Equal(h.cfg.APIKeys, []string{"anchor", "first", "second"}) {
		t.Fatalf("keys after stale reload = %v", h.cfg.APIKeys)
	}
}
