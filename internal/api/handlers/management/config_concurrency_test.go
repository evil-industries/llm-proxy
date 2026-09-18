package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func concurrentManagementCalls(calls ...func()) {
	var workers sync.WaitGroup
	start := make(chan struct{})
	for _, call := range calls {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			call()
		}()
	}
	close(start)
	workers.Wait()
}

func configManagementRequest(method, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/v0/management/config", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return w
}

func TestManagementAuthenticationDuringConfigReload(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, envSecret: "test-management-key", failedAttempts: make(map[string]*attemptInfo)}
	concurrentManagementCalls(func() {
		for i := 0; i < 2000; i++ {
			h.SetConfig(&config.Config{RemoteManagement: config.RemoteManagement{AllowRemote: i%2 == 0}})
			h.SetLocalPassword(strconv.Itoa(i))
		}
	}, func() {
		for i := 0; i < 2000; i++ {
			ok, status, message := h.AuthenticateManagementKey("127.0.0.1", true, "test-management-key")
			if !ok {
				t.Errorf("valid management key rejected during reload: %d %s", status, message)
				return
			}
		}
	})
}

func TestManagementConfigReadersDuringUpdates(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}
	h.cfg.AuthDir = t.TempDir()
	readers := []gin.HandlerFunc{h.GetConfig, h.GetDebug, h.GetUsageStatisticsEnabled, h.GetLoggingToFile, h.GetRequestLog, h.GetWebsocketAuth, h.GetRoutingStrategy, h.GetSwitchProject, h.GetSwitchPreviewModel, h.GetLogs, h.ListAuthFiles}
	concurrentManagementCalls(func() {
		for i := 0; i < 30; i++ {
			response := configManagementRequest(http.MethodPut, `{"value":true}`, h.PutDebug)
			if response.Code != http.StatusOK {
				t.Errorf("setting save failed: %s", response.Body.String())
				return
			}
			response = configManagementRequest(http.MethodPatch, `{"provider":"codex","models":["model-a"]}`, h.PatchOAuthExcludedModels)
			if response.Code != http.StatusOK {
				t.Errorf("map update failed: %s", response.Body.String())
				return
			}
		}
	}, func() {
		for i := 0; i < 100; i++ {
			for _, reader := range readers {
				response := configManagementRequest(http.MethodGet, "", reader)
				if !json.Valid(response.Body.Bytes()) {
					t.Errorf("invalid JSON while config changes: %s", response.Body.String())
					return
				}
			}
		}
	}, func() {
		manager := coreauth.NewManager(nil, nil, nil)
		for i := 0; i < 1000; i++ {
			h.SetAuthManager(manager)
		}
	})
}

func TestManagementConfigSnapshotOwnsNestedValues(t *testing.T) {
	h := &Handler{cfg: &config.Config{SDKConfig: config.SDKConfig{APIKeys: []string{"first"}}, OAuthExcludedModels: map[string][]string{"codex": {"before"}}}}
	snapshot := h.configSnapshot()
	h.mu.Lock()
	h.cfg.APIKeys[0] = "second"
	h.cfg.OAuthExcludedModels["codex"][0] = "after"
	h.mu.Unlock()
	if snapshot.APIKeys[0] != "first" || snapshot.OAuthExcludedModels["codex"][0] != "before" {
		t.Fatal("configuration readers must own nested slices and maps after releasing the mutex")
	}
}

func TestManagementScalarSavePreservesPublishedConfigAndRollsBack(t *testing.T) {
	previous := &config.Config{}
	h := &Handler{cfg: previous, configFilePath: writeTestConfigFile(t)}
	response := configManagementRequest(http.MethodPut, `{"value":true}`, h.PutDebug)
	if response.Code != http.StatusOK || previous.Debug || !h.configSnapshot().Debug {
		t.Fatal("scalar save must preserve the previously published config")
	}
	h.configFilePath = filepath.Join(t.TempDir(), "missing", "config.yaml")
	response = configManagementRequest(http.MethodPut, `{"value":false}`, h.PutDebug)
	if response.Code != http.StatusInternalServerError || !h.configSnapshot().Debug {
		t.Fatal("failed scalar save must restore the previous config")
	}
}
