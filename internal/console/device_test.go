package console

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceLoginSessionIsolationResumeSanitizationAndRevocation(t *testing.T) {
	var starts, polls atomic.Int32
	cancelled := make(chan string, 2)
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/device-auth" {
			t.Errorf("unexpected device path %s", r.URL)
			w.WriteHeader(500)
			return
		}
		if r.Method == "DELETE" {
			cancelled <- r.URL.Query().Get("state")
			jsonResponse(w, 200, map[string]any{"status": "cancelled"})
			return
		}
		if r.Method == "POST" {
			starts.Add(1)
			body, _ := io.ReadAll(r.Body)
			if string(body) != "{}" {
				t.Error("browser body forwarded")
			}
		} else {
			polls.Add(1)
		}
		jsonResponse(w, 200, map[string]any{"state": "private-backend-state-123456", "status": "pending", "user_code": "ABCD-EFGH", "verification_uri": "https://auth.openai.com/codex/device", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "interval": 5, "access_token": "private-provider-token", "device_auth_id": "private-device"})
	})
	h := fixture(t, backend)
	token := newToken(t, h)
	other := newToken(t, h)
	// Simulate real session issuance so revocation also schedules flow cancellation.
	v := h.sessions.get(token)
	stop := context.AfterFunc(v.ctx, func() { h.forgetDevice(v) })
	defer stop()
	checkStatus(t, request(h, "GET", "/api/codex/device-auth?state=stolen", token, ""), 400)
	checkStatus(t, request(h, "POST", "/api/codex/device-auth", "", "{}"), 401)
	w := request(h, "POST", "/api/codex/device-auth", token, `{"state":"attacker"}`)
	checkStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "ABCD-EFGH") {
		t.Fatal("approval code missing")
	}
	for _, secret := range []string{"private-", "state", "access_token", "device_auth_id"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("%s exposed in %s", secret, w.Body)
		}
	}
	w = request(h, "GET", "/api/codex/device-auth", other, "")
	if !strings.Contains(w.Body.String(), "idle") || polls.Load() != 0 {
		t.Fatal("another session obtained device flow")
	}
	checkStatus(t, request(h, "POST", "/api/codex/device-auth", token, "{}"), 200)
	if starts.Load() != 1 || polls.Load() != 1 {
		t.Fatal("repeat start orphaned a flow")
	}
	checkStatus(t, request(h, "DELETE", "/api/session", token, ""), 200)
	select {
	case state := <-cancelled:
		if state != "private-backend-state-123456" {
			t.Fatal("wrong flow cancelled")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("logout did not cancel device flow")
	}
	checkStatus(t, request(h, "GET", "/api/codex/device-auth", token, ""), 401)
}
