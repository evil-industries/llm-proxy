package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

const consoleTestHash = "scrypt$131072$8$1$00112233445566778899aabbccddeeff$d3cec985677cee8d187382f5699004d5303cd0551fbff6c59619d67348416063aae68b723f7bb26779f86db1ed4cf0ebad5555609e9382bb55ec66d53e101942"

func consoleServer(t *testing.T) (*Server, *http.Cookie) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>console-shell"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONSOLE_ENABLED", "true")
	t.Setenv("CONSOLE_STATIC_DIR", dir)
	t.Setenv("AUTH_MODE", "password")
	t.Setenv("ORIGIN", "https://console.example")
	t.Setenv("AUTH_PASSWORD_HASH", consoleTestHash)
	// The console is independent of legacy management secrets and networking.
	t.Setenv("MANAGEMENT_PASSWORD", "")
	s := newTestServer(t)
	t.Cleanup(func() { s.console.Close(); s.mgmt.CloseEventStreams(); s.mgmt.CancelDeviceAuthFlows() })
	s.cfg.ProxyURL = "http://private-user:provider-private-secret@proxy.example:8080"
	if err := os.WriteFile(s.configFilePath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveConfigPreserveComments(s.configFilePath, s.cfg); err != nil {
		t.Fatal(err)
	}
	response := consoleRequest(s, "POST", "/api/session", nil, strings.NewReader(`{"password":"a-long-test-password"}`), "application/json")
	if response.Code != 200 {
		t.Fatalf("login: %d %s", response.Code, response.Body)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "__Host-management_session" && cookie.MaxAge > 0 {
			return s, cookie
		}
	}
	t.Fatal("no session")
	return nil, nil
}
func consoleRequest(s *Server, method, p string, cookie *http.Cookie, body io.Reader, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://console.example"+p, body)
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("Origin", "https://console.example")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestStaticConsoleRealManagementAndPersistence(t *testing.T) {
	s, cookie := consoleServer(t)
	for _, p := range []string{"/", "/login", "/settings/deep-link", "/settings/deep-link/"} {
		w := consoleRequest(s, "GET", p, nil, nil, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "console-shell") {
			t.Fatalf("SPA %s: %d %s", p, w.Code, w.Body)
		}
	}
	w := consoleRequest(s, "GET", "/api/management/config", nil, nil, "")
	if w.Code != 401 {
		t.Fatalf("unauthorized: %d", w.Code)
	}
	for _, p := range []string{"/healthz", "/api/management/config", "/api/management/throughput", "/api/management/auth-files", "/api/management/notifications", "/api/management/api-keys", "/api/management/routing/strategy"} {
		w = consoleRequest(s, "GET", p, cookie, nil, "")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "provider-private-secret") {
			t.Fatalf("secret in %s", p)
		}
	}
	// Preserve registered extension routes, including /api paths, through the wrapper.
	s.engine.GET("/api/extension", func(c *gin.Context) { c.Status(202) })
	if w = consoleRequest(s, "GET", "/api/extension", nil, nil, ""); w.Code != 202 {
		t.Fatalf("extension route hijacked: %d", w.Code)
	}
	for _, p := range []string{"/api/management/config.yaml", "/api/management/auth-files/download", "/api/management/api-call", "/v0/management/config", "/management.html"} {
		w = consoleRequest(s, "GET", p, cookie, nil, "")
		if w.Code != 404 {
			t.Fatalf("scope %s: %d", p, w.Code)
		}
	}
	for _, change := range []struct{ p, body string }{{"usage-statistics-enabled", `{"value":true}`}, {"logging-to-file", `{"value":true}`}, {"request-log", `{"value":true}`}, {"ws-auth", `{"value":true}`}, {"routing/strategy", `{"value":"round-robin"}`}} {
		w = consoleRequest(s, "PUT", "/api/management/"+change.p, cookie, strings.NewReader(change.body), "application/json")
		if w.Code != 200 {
			t.Fatalf("setting %s: %d %s", change.p, w.Code, w.Body)
		}
	}
	w = consoleRequest(s, "POST", "/api/management/api-keys/mutate", cookie, strings.NewReader(`{"action":"add","value":"new-client-key"}`), "application/json")
	if w.Code != 200 {
		t.Fatalf("key mutation: %d %s", w.Code, w.Body)
	}
	// Upload a real credential and confirm it persists without returning its token.
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	part, err := form.CreateFormFile("file", "test-account.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, `{"type":"codex","email":"fixture@example.com","access_token":"private-provider-token","refresh_token":"private-refresh-token"}`)
	if errClose := form.Close(); errClose != nil {
		t.Fatal(errClose)
	}
	w = consoleRequest(s, "POST", "/api/management/auth-files", cookie, &upload, form.FormDataContentType())
	if w.Code != 200 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	contents, err := os.ReadFile(filepath.Join(s.cfg.AuthDir, "test-account.json"))
	if err != nil || !bytes.Contains(contents, []byte("private-provider-token")) {
		t.Fatalf("credential not persisted: %v", err)
	}
	w = consoleRequest(s, "GET", "/api/management/auth-files", cookie, nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "test-account.json") || strings.Contains(w.Body.String(), "private-provider-token") || strings.Contains(w.Body.String(), s.cfg.AuthDir) {
		t.Fatalf("account view: %d %s", w.Code, w.Body)
	}
	w = consoleRequest(s, "PATCH", "/api/management/auth-files/status", cookie, strings.NewReader(`{"name":"test-account.json","disabled":true}`), "application/json")
	if w.Code != 200 {
		t.Fatalf("toggle: %d %s", w.Code, w.Body)
	}
	// Reload the same config file: mutations survive a new server process.
	persisted, err := config.LoadConfig(s.configFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.RequestLog || !persisted.WebsocketAuth || !persisted.LoggingToFile || !persisted.UsageStatisticsEnabled || len(persisted.APIKeys) != 2 || persisted.APIKeys[1] != "new-client-key" {
		t.Fatalf("settings not persisted: %+v", persisted.APIKeys)
	}
	// Existing upstream-compatible config output is unchanged and remains private.
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/config", nil)
	s.mgmt.GetConfig(c)
	if !strings.Contains(recorder.Body.String(), "provider-private-secret") {
		t.Fatal("legacy config schema changed")
	}
	w = consoleRequest(s, "DELETE", "/api/management/auth-files?name=test-account.json", cookie, nil, "")
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err = os.Stat(filepath.Join(s.cfg.AuthDir, "test-account.json")); !os.IsNotExist(err) {
		t.Fatal("credential not deleted")
	}
}

func TestStaticConsoleRealLogsArchivesEventsAndLogout(t *testing.T) {
	s, cookie := consoleServer(t)
	s.cfg.LoggingToFile = true
	logDir := t.TempDir()
	s.mgmt.SetLogDirectory(logDir)
	name := "v1-chat-completions-2026-10-27T120000-fixture.log"
	payload := strings.Repeat("request fixture\n", 12000)
	for file, data := range map[string]string{"main.log": "2026-10-27 12:00:00 info fixture log\n", name: payload} {
		if err := os.WriteFile(filepath.Join(logDir, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"logs?limit=10", "request-logs", "request-logs/" + name + "?offset=0&limit=1024", "log-archives?limit=10"} {
		w := consoleRequest(s, "GET", "/api/management/"+p, cookie, nil, "")
		if w.Code != 200 {
			t.Fatalf("log %s: %d %s", p, w.Code, w.Body)
		}
	}
	for _, p := range []string{"/api/management/request-logs/" + name + "/download", "/api/management/log-archives/download?source=local&key=" + name} {
		w := consoleRequest(s, "GET", p, cookie, nil, "")
		if w.Code != 200 || w.Body.String() != payload || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
			t.Fatalf("download: %s %d", p, w.Code)
		}
	}
	// Exercise the actual Go SSE handler through the console, not a buffered recorder.
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/api/management/events", nil)
	req.Host = "console.example"
	req.AddCookie(cookie)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events: %d", response.StatusCode)
	}
	first := make([]byte, len(": connected\n\n"))
	if _, err = io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	w := consoleRequest(s, "DELETE", "/api/session", cookie, nil, "")
	if w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(response.Body); done <- data }()
	select {
	case data := <-done:
		if !bytes.Contains(data, []byte("session-ended")) {
			t.Fatalf("missing logout event: %s", data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("events still open after logout")
	}
	if w = consoleRequest(s, "GET", "/api/management/logs", cookie, nil, ""); w.Code != 401 {
		t.Fatal("revoked cookie accepted")
	}
}

func TestStaticConsoleFailsClosedAndDoesNotRequireNetworkManagement(t *testing.T) {
	s, _ := consoleServer(t)
	if s.managementRoutesRegistered.Load() {
		t.Fatal("console enabled legacy network management")
	}
	// Config/identity errors do not break the inference/health handler.
	s.console.Close()
	t.Setenv("AUTH_PASSWORD_HASH", "invalid")
	handler := s.setupConsole(s.engine)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "https://console.example/api/session", nil))
	if w.Code != 503 {
		t.Fatalf("invalid auth not closed: %d", w.Code)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatalf("health disabled: %d", w.Code)
	}
	// Home configuration must not expose local console operations.
	s.cfg.Home.Enabled = true
	w = consoleRequest(s, "GET", "/api/session", nil, nil, "")
	if w.Code != 503 {
		t.Fatalf("Home console: %d", w.Code)
	}
}
