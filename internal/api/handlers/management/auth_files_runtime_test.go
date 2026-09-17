package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRuntimeAuthFileStatusRemainsReversibleUntilRemoval(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{
		ID: "aistudio-runtime", Provider: "aistudio", Status: coreauth.StatusActive,
		Attributes: map[string]string{"runtime_only": "true"},
	}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register runtime credential: %v", errRegister)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	type runtimeEntry struct {
		Name        string `json:"name"`
		AuthIndex   string `json:"auth_index"`
		Disabled    bool   `json:"disabled"`
		RuntimeOnly bool   `json:"runtime_only"`
	}
	list := func() []runtimeEntry {
		t.Helper()
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
		h.ListAuthFiles(ctx)
		if rec.Code != http.StatusOK {
			t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Files []runtimeEntry `json:"files"`
		}
		if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
			t.Fatalf("decode list: %v", errDecode)
		}
		return body.Files
	}
	for _, disabled := range []bool{true, false} {
		files := list()
		if len(files) != 1 || files[0].Name != auth.ID || !files[0].RuntimeOnly || files[0].AuthIndex == "" {
			t.Fatalf("expected identifiable runtime credential before change: %+v", files)
		}
		body, errEncode := json.Marshal(map[string]any{"name": files[0].Name, "auth_index": files[0].AuthIndex, "disabled": disabled})
		if errEncode != nil {
			t.Fatal(errEncode)
		}
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/status", strings.NewReader(string(body)))
		ctx.Request.Header.Set("Content-Type", "application/json")
		h.PatchAuthFileStatus(ctx)
		if rec.Code != http.StatusOK {
			t.Fatalf("change status = %d: %s", rec.Code, rec.Body.String())
		}
		files = list()
		if len(files) != 1 || files[0].Disabled != disabled {
			t.Fatalf("expected visible credential disabled=%v: %+v", disabled, files)
		}
	}
	// WebSocket disconnection removes the runtime entry rather than disabling it.
	manager.Remove(context.Background(), auth.ID)
	if files := list(); len(files) != 0 {
		t.Fatalf("removed runtime credential remains visible: %+v", files)
	}
}
