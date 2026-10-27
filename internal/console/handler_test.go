package console

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testPasswordHash = "scrypt$131072$8$1$00112233445566778899aabbccddeeff$d3cec985677cee8d187382f5699004d5303cd0551fbff6c59619d67348416063aae68b723f7bb26779f86db1ed4cf0ebad5555609e9382bb55ec66d53e101942"

func fixture(t *testing.T, backend http.Handler) *Handler {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "_app/immutable"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"index.html": "<!doctype html><h1>static console</h1>", "_app/immutable/app.js": "console.log('static asset')"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if backend == nil {
		backend = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected backend call %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		})
	}
	h := New(Config{Origin: "https://console.example", StaticDir: dir, PasswordHash: testPasswordHash}, backend, nil)
	t.Cleanup(h.Close)
	return h
}
func request(h http.Handler, method, p, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://console.example"+p, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.1:1234"
	r.Header.Set("Origin", "https://console.example")
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "__Host-management_session", Value: token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func newToken(t *testing.T, h *Handler) string {
	t.Helper()
	token, v := h.sessions.create("test")
	if v == nil {
		t.Fatal("no session")
	}
	return token
}
func checkStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body)
	}
}

func TestConfigurationAndNoSecretErrors(t *testing.T) {
	env := map[string]string{"ORIGIN": "https://console.example", "AUTH_PASSWORD_HASH": testPasswordHash}
	c, err := Load(func(k string) string { return env[k] })
	if err != nil || c.PasswordHash != testPasswordHash {
		t.Fatalf("load: %v", err)
	}
	for key, value := range map[string]string{"ORIGIN": "http://public.example", "AUTH_PASSWORD_HASH": "secret-invalid-hash", "AUTH_MODE": "invalid-secret"} {
		previous := env[key]
		env[key] = value
		_, err = Load(func(k string) string { return env[k] })
		if err == nil || strings.Contains(err.Error(), value) {
			t.Fatalf("expected fixed validation error for %s", key)
		}
		env[key] = previous
	}
	for _, origin := range []string{"http://127.0.0.1:5173", "http://[::1]:5173", "http://localhost:5173", "https://llm.rose.sh"} {
		env["ORIGIN"] = origin
		if _, err = Load(func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
	}
	env["AUTH_MODE"] = "oidc"
	env["OIDC_ISSUER"] = "https://auth.rose.sh"
	env["OIDC_CLIENT_ID"] = "console"
	env["OIDC_CLIENT_SECRET"] = "fixture-private-secret"
	env["OIDC_ALLOWED_USERS"] = "alice, bob"
	c, err = Load(func(k string) string { return env[k] })
	if err != nil || c.PasswordHash != "" || len(c.AllowedUsers) != 2 {
		t.Fatalf("oidc: %+v %v", c, err)
	}
	delete(env, "OIDC_ALLOWED_USERS")
	if _, err = Load(func(k string) string { return env[k] }); err == nil {
		t.Fatal("missing authorization policy accepted")
	}
}

func TestPasswordSessionsAndCSRF(t *testing.T) {
	h := fixture(t, nil)
	checkStatus(t, request(h, "GET", "/api/management/config", "", ""), 401)
	checkStatus(t, request(h, "POST", "/api/session", "", `{"password":"wrong"}`), 401)
	login := request(h, "POST", "/api/session", "", `{"password":"a-long-test-password"}`)
	checkStatus(t, login, 200)
	var cookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == "__Host-management_session" && c.MaxAge > 0 {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 28800 {
		t.Fatalf("bad cookie: %+v", cookie)
	}
	if strings.Contains(login.Body.String(), cookie.Value) || strings.Contains(login.Body.String(), testPasswordHash) {
		t.Fatal("secret in login response")
	}
	checkStatus(t, request(h, "GET", "/api/session", cookie.Value, ""), 200)
	for _, origin := range []string{"", "null", "https://evil.example"} {
		r := httptest.NewRequest("DELETE", "https://console.example/api/session", nil)
		r.Header.Set("Origin", origin)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		checkStatus(t, w, 403)
		if h.sessions.get(cookie.Value) == nil {
			t.Fatal("cross-origin logout revoked session")
		}
	}
	next := request(h, "POST", "/api/session", cookie.Value, `{"password":"a-long-test-password"}`)
	checkStatus(t, next, 200)
	if h.sessions.get(cookie.Value) != nil {
		t.Fatal("old session was not rotated")
	}
	for _, c := range next.Result().Cookies() {
		if c.Name == cookie.Name && c.MaxAge > 0 {
			cookie = c
		}
	}
	checkStatus(t, request(h, "DELETE", "/api/session", cookie.Value, ""), 200)
	if h.sessions.get(cookie.Value) != nil {
		t.Fatal("logout did not revoke")
	}
	checkStatus(t, request(h, "GET", "/api/management/config", cookie.Value, ""), 401)
	// A process restart never accepts an old cookie.
	checkStatus(t, request(fixture(t, nil), "GET", "/api/management/config", cookie.Value, ""), 401)
}

func TestSessionExpiryLimitsAndLoginBody(t *testing.T) {
	h := fixture(t, nil)
	now := time.Now()
	h.sessions.now = func() time.Time { return now }
	token, v := h.sessions.create("address")
	now = now.Add(sessionLifetime)
	if h.sessions.get(token) != nil || v.ctx.Err() == nil {
		t.Fatal("expired session remained usable")
	}
	for i := 0; i < 5; i++ {
		if !h.sessions.permit("address") {
			t.Fatal("early throttle")
		}
	}
	if h.sessions.permit("address") {
		t.Fatal("attempt limit ignored")
	}
	now = now.Add(15 * time.Minute)
	if !h.sessions.permit("address") {
		t.Fatal("throttle did not expire")
	}
	h.sessions.verifying <- struct{}{}
	h.sessions.verifying <- struct{}{}
	checkStatus(t, request(h, "POST", "/api/session", "", `{"password":"some-password"}`), 503)
	<-h.sessions.verifying
	<-h.sessions.verifying
	for _, body := range []string{`{}`, `{"password":42}`, `{"password":""}`, `{} {}`, strings.Repeat("x", 4097)} {
		clear(h.sessions.attempts)
		checkStatus(t, request(h, "POST", "/api/session", "", body), 400)
	}
	// Bounded stores never evict another user's live session/transaction.
	for len(h.sessions.entries) < maxEntries {
		h.sessions.create("address")
	}
	if token, _ := h.sessions.create("address"); token != "" {
		t.Fatal("session limit ignored")
	}
}

func TestStaticDeepLinksAssetsAndBackendIsolation(t *testing.T) {
	h := fixture(t, nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !backendPath(r.URL.Path) && !strings.HasPrefix(r.URL.Path, "/v0") {
			h.Fallback(w, r)
			return
		}
		w.WriteHeader(202)
	})
	wrapped := h.Wrap(next)
	for _, p := range []string{"/", "/login", "/credentials/deep-link"} {
		w := request(wrapped, "GET", p, "", "")
		checkStatus(t, w, 200)
		if !strings.Contains(w.Body.String(), "static console") {
			t.Fatal("missing SPA fallback")
		}
	}
	w := request(wrapped, "GET", "/_app/immutable/app.js", "", "")
	checkStatus(t, w, 200)
	if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("bad asset headers: %v", w.Header())
	}
	for _, p := range []string{"/_app/missing.js", "/missing.css", "/.env", "/../config.yaml", "/management.html", "/v0/management/config", "/v0/resource/plugins/test"} {
		checkStatus(t, request(wrapped, "GET", p, "", ""), 404)
	}
	for _, p := range []string{"/v1/chat/completions", "/v1/responses", "/v1beta/models", "/openai/v1/models", "/backend-api/codex/responses", "/healthz", "/callback"} {
		checkStatus(t, request(wrapped, "POST", p, "", ""), 202)
	}
	for _, host := range []string{"evil.example", "console.example.evil"} {
		r := httptest.NewRequest("GET", "https://"+host+"/api/session", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		checkStatus(t, w, 403)
	}
	// Only a direct local management caller can reach the legacy authenticated API.
	r := httptest.NewRequest("GET", "http://console.example/v0/management/config", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	checkStatus(t, w, 202)
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, r)
	checkStatus(t, w, 404)
}

func TestManagementScopeQueriesUploadsAndSecretFiltering(t *testing.T) {
	var calls int
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Forwarded-For") != "" {
			t.Error("browser credentials forwarded")
		}
		w.Header().Set("Set-Cookie", "server-secret=value")
		w.Header().Set("X-Private-Token", "private-secret")
		if r.URL.Path == "/auth-files" && r.Method == "GET" {
			jsonResponse(w, 200, map[string]any{"files": []any{map[string]any{"name": "account.json", "quota": map[string]any{"signals": map[string]string{"x-codex-plan-type": "plus"}}, "path": "/secret/provider-accounts/account.json", "access_token": "private-token", "quota_probe": map[string]any{"token": "secret"}, "id_token": map[string]any{"plan_type": "plus", "private": "secret"}}}})
			return
		}
		if r.Method == "POST" {
			payload, err := io.ReadAll(r.Body)
			if err != nil || len(payload) < 6*1024*1024 {
				t.Error("upload was truncated")
			}
		}
		if r.URL.Path == "/debug" {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, "private-error-secret")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/download") {
			_, _ = io.WriteString(w, strings.Repeat("log data", 1024))
			return
		}
		jsonResponse(w, 200, map[string]any{"ok": true})
	})
	h := fixture(t, backend)
	token := newToken(t, h)
	for _, p := range []string{"../config", "config.yaml", "auth-files/download", "api-call", "usage-queue", "plugins", "codex/device-auth", "config/"} {
		checkStatus(t, request(h, "GET", "/api/management/"+p, token, ""), 404)
	}
	for _, p := range []string{"config?key=secret", "auth-files?all=true", "auth-files?name=a&name=b", "logs?limit=%ZZ", "log-archives?limit=1001", "log-archives/download?source=local&key=../secret", "log-archives/download?source=s3&key=a//b", "events?cursor=1"} {
		checkStatus(t, request(h, "GET", "/api/management/"+p, token, ""), 400)
	}
	checkStatus(t, request(h, "PUT", "/api/management/config", token, "{}"), 404)
	checkStatus(t, request(h, "DELETE", "/api/management/auth-files?all=true", token, ""), 400)
	checkStatus(t, request(h, "DELETE", "/api/management/api-keys?index=-1", token, ""), 400)
	if calls != 0 {
		t.Fatal("denied request dispatched")
	}
	w := request(h, "GET", "/api/management/auth-files", token, "")
	checkStatus(t, w, 200)
	for _, secret := range []string{"private-token", "/secret", "quota_probe", "private\""} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("account secret %s in %s", secret, w.Body)
		}
	}
	if !strings.Contains(w.Body.String(), "plus") {
		t.Fatal("plan lost")
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("X-Private-Token") != "" {
		t.Fatal("private headers leaked")
	}
	checkStatus(t, request(h, "POST", "/api/management/auth-files", token, strings.Repeat("x", 6*1024*1024)), 200)
	w = request(h, "PUT", "/api/management/debug", token, "{}")
	checkStatus(t, w, 502)
	if strings.Contains(w.Body.String(), "private-error") {
		t.Fatal("error secret leaked")
	}
	w = request(h, "GET", "/api/management/log-archives/download?source=s3&key=logs%2Farchive.gz", token, "")
	checkStatus(t, w, 200)
	if w.Body.Len() != 8192 || !strings.Contains(w.Header().Get("Content-Disposition"), "archive.gz") || w.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("bad download: %d %v", w.Body.Len(), w.Header())
	}
}

func TestSSELogoutAndExpiryCancelWithoutPolling(t *testing.T) {
	for _, end := range []string{"logout", "expiry", "shutdown"} {
		t.Run(end, func(t *testing.T) {
			h := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Accel-Buffering", "no")
				_, _ = io.WriteString(w, "data: 1\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				if _, errWrite := io.WriteString(w, "data: late-secret\n\n"); errWrite == nil {
					t.Error("revoked stream accepted more data")
				}
			}))
			var now atomic.Int64
			now.Store(time.Now().UnixNano())
			h.sessions.now = func() time.Time { return time.Unix(0, now.Load()) }
			token := newToken(t, h)
			server := httptest.NewServer(h)
			defer server.Close()
			h.config.Origin = server.URL
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/management/events", nil)
			r.AddCookie(&http.Cookie{Name: "management_session", Value: token})
			response, err := server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 200 || response.Header.Get("X-Accel-Buffering") != "no" {
				t.Fatalf("stream headers: %v", response)
			}
			reader := bufio.NewReader(response.Body)
			line, err := reader.ReadString('\n')
			if err != nil || line != "data: 1\n" {
				t.Fatalf("no immediate event: %q %v", line, err)
			}
			switch end {
			case "logout":
				h.sessions.revoke(token)
			case "expiry":
				now.Add(int64(sessionLifetime))
				h.sessions.get(token)
			case "shutdown":
				h.Close()
			}
			done := make(chan []byte, 1)
			go func() { data, _ := io.ReadAll(reader); done <- data }()
			select {
			case data := <-done:
				if !bytes.Contains(data, []byte("session-ended")) {
					t.Fatalf("missing terminal event: %q", data)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("stream did not close")
			}
		})
	}
}

func TestInferenceStreamingAndWebsocketPassThrough(t *testing.T) {
	upgrader := websocket.Upgrader{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			kind, data, err := conn.ReadMessage()
			if err == nil {
				_ = conn.WriteMessage(kind, data)
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	h := fixture(t, nil)
	server := httptest.NewServer(h.Wrap(next))
	defer server.Close()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	if err != nil {
		t.Fatalf("upgrade: %v %+v", err, response)
	}
	defer conn.Close()
	if err = conn.WriteMessage(websocket.TextMessage, []byte("frame")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil || string(data) != "frame" {
		t.Fatalf("frame: %s %v", data, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("buffered inference %q %v", line, err)
	}
}
