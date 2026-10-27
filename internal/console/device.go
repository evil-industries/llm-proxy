package console

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"time"
)

var statePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,256}$`)

func (h *Handler) device(w http.ResponseWriter, r *http.Request, v *session) {
	if !slices.Contains([]string{"GET", "POST", "DELETE"}, r.Method) {
		w.WriteHeader(405)
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "Device login is bound to your console session.")
		return
	}
	v.deviceMu.Lock()
	defer v.deviceMu.Unlock()
	if v.ctx.Err() != nil {
		failure(w, 401, "Session ended.")
		return
	}
	if v.deviceResult == nil {
		v.deviceResult = map[string]any{"status": "idle"}
	}
	method := r.Method
	if v.deviceState != "" && v.deviceResult["status"] == "pending" {
		operation := "GET"
		if method == "DELETE" {
			operation = "DELETE"
		}
		current, status := h.deviceCall(r, operation, v.deviceState)
		if status == 404 {
			current = map[string]any{"status": "error"}
		} else if status >= 400 {
			failure(w, 502, "The gateway could not check this login attempt. Try again.")
			return
		}
		result, ok := publicDevice(current)
		if !ok {
			failure(w, 502, "The gateway returned an invalid device login response.")
			return
		}
		v.deviceResult = result
		if method != "POST" || result["status"] == "pending" || result["status"] == "complete" {
			jsonResponse(w, 200, result)
			return
		}
	}
	if method == "POST" {
		data, status := h.deviceCall(r, "POST", "")
		if status >= 400 {
			failure(w, 502, "Device login could not start. Check your gateway connection and enable device code login in your ChatGPT security settings.")
			return
		}
		state, ok := data["state"].(string)
		result, valid := publicDevice(data)
		if !ok || !statePattern.MatchString(state) || !valid {
			failure(w, 502, "The gateway returned an invalid device login response.")
			return
		}
		v.deviceState = state
		v.deviceResult = result
	}
	jsonResponse(w, 200, v.deviceResult)
}
func publicDevice(data map[string]any) (map[string]any, bool) {
	status, _ := data["status"].(string)
	if !slices.Contains([]string{"idle", "pending", "complete", "error", "expired", "cancelled"}, status) {
		return nil, false
	}
	result := map[string]any{"status": status}
	if status == "pending" {
		code, _ := data["user_code"].(string)
		uri, _ := data["verification_uri"].(string)
		expires, _ := data["expires_at"].(string)
		interval, _ := data["interval"].(float64)
		if _, err := time.Parse(time.RFC3339Nano, expires); err != nil || code == "" || uri != "https://auth.openai.com/codex/device" || interval < 1 {
			return nil, false
		}
		result["user_code"], result["verification_uri"], result["expires_at"], result["interval"] = code, uri, expires, interval
	}
	if account, ok := data["account"].(map[string]any); ok {
		clean := map[string]any{}
		for _, key := range []string{"name", "email", "plan_type"} {
			if value, valid := account[key].(string); valid {
				clean[key] = value
			}
		}
		result["account"] = clean
	}
	if status == "error" {
		result["error"] = "Device login failed. Check your account permissions and start a new login."
	}
	if status == "expired" {
		result["error"] = "The device code expired. Start a new login to get another code."
	}
	return result, true
}
func (h *Handler) forgetDevice(v *session) {
	v.deviceMu.Lock()
	defer v.deviceMu.Unlock()
	if v.deviceState == "" || v.deviceResult["status"] != "pending" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "DELETE", "http://console.internal/codex/device-auth", nil)
	h.deviceCall(r, "DELETE", v.deviceState)
	v.deviceState = ""
}
