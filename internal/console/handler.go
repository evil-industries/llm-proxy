package console

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementevents"
	log "github.com/sirupsen/logrus"
)

type Handler struct {
	config     Config
	backend    http.Handler
	sessions   *sessions
	files      fs.FS
	providerMu sync.Mutex
	provider   *oidc.Provider
	authClient *http.Client
	available  func() bool
}

func New(c Config, backend http.Handler, available func() bool) *Handler {
	return &Handler{config: c, backend: backend, sessions: newSessions(), files: os.DirFS(c.StaticDir), authClient: &http.Client{Timeout: 10 * time.Second}, available: available}
}
func (h *Handler) Close() { h.sessions.close() }

// Wrap keeps inference on the existing handler, including its WebSocket upgrader.
// Raw administration is local-only while the console is enabled, even if a legacy
// MANAGEMENT_PASSWORD would otherwise enable remote management.
func (h *Handler) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/v0" || strings.HasPrefix(p, "/v0/") || p == "/management.html" {
			if p == "/management.html" || !isLoopback(peerAddress(r)) || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		// Only console-owned routes are intercepted. Registered extension and
		// inference routes keep their original middleware and WebSocket behavior.
		if !consoleRoute(p) {
			next.ServeHTTP(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}
func consoleRoute(p string) bool {
	return p == "/" || p == "/login" || p == "/api/session" || p == "/api/codex/device-auth" || strings.HasPrefix(p, "/api/management/") || strings.HasPrefix(p, "/auth/oidc/") || strings.HasPrefix(p, "/_app/")
}

// Fallback is used only after the main router has found no registered route.
// API typos and missing assets must never become a successful HTML response.
func (h *Handler) Fallback(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if backendPath(p) || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/auth/") || strings.HasPrefix(p, "/v0/") {
		return false
	}
	h.ServeHTTP(w, r)
	return true
}
func backendPath(p string) bool {
	for _, prefix := range []string{"/callback", "/v1", "/v1beta", "/openai", "/backend-api", "/healthz", "/antigravity", "/oauth2callback", "/google/callback", "/anthropic/callback", "/codex/callback", "/kimi/callback", "/qwen/callback", "/iflow/callback", "/xai/callback", "/devin/callback", "/meta/callback"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Debug("console: response connection closed")
	}
}
func failure(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]any{"error": message})
}
func (h *Handler) sameOrigin(r *http.Request) bool { return r.Header.Get("Origin") == h.config.Origin }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	if strings.HasPrefix(r.URL.Path, "/auth/") {
		w.Header().Set("Referrer-Policy", "no-referrer")
	}
	// Host is checked without trusting any forwarded header. TLS termination does
	// not change the externally configured origin or the cookie's Secure flag.
	if h.config.Origin == "" || h.available != nil && !h.available() {
		failure(w, 503, "Management is not configured or unavailable.")
		return
	}
	originHost := strings.SplitN(h.config.Origin, "://", 2)[1]
	if r.Host != originHost {
		failure(w, 403, "Request host was not accepted.")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" && !h.sameOrigin(r) {
		failure(w, 403, "Request origin was not accepted.")
		return
	}
	p := r.URL.Path
	if p == "/api/session" {
		h.session(w, r)
		return
	}
	if p == "/auth/oidc/login" && r.Method == "POST" {
		h.oidcLogin(w, r)
		return
	}
	if p == "/auth/oidc/callback" && r.Method == "GET" {
		h.oidcCallback(w, r)
		return
	}
	if strings.HasPrefix(p, "/api/") {
		v := h.sessions.get(h.cookie(r, "session"))
		if v == nil {
			failure(w, 401, "Sign in to continue.")
			return
		}
		// In-flight operations and SSE are revoked on logout and absolute expiry.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stop := context.AfterFunc(v.ctx, cancel)
		defer stop()
		timer := time.AfterFunc(v.expires.Sub(h.sessions.now()), cancel)
		defer timer.Stop()
		r = r.WithContext(ctx)
		switch {
		case p == "/api/codex/device-auth":
			h.device(w, r, v)
		case strings.HasPrefix(p, "/api/management/"):
			h.management(w, r, v)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if strings.HasPrefix(p, "/auth/") {
		http.NotFound(w, r)
		return
	}
	h.static(w, r)
}

func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		jsonResponse(w, 200, map[string]any{"authenticated": h.sessions.get(h.cookie(r, "session")) != nil, "configured": true, "oidc": h.config.Issuer != ""})
	case "DELETE":
		h.revoke(w, r)
		jsonResponse(w, 200, map[string]any{"authenticated": false})
	case "POST":
		if h.config.Issuer != "" {
			failure(w, 403, "Use Authelia to sign in.")
			return
		}
		media, _, errType := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if errType != nil || media != "application/json" {
			failure(w, 415, "Send the password as JSON.")
			return
		}
		if !h.sessions.permit(peerAddress(r)) {
			failure(w, 429, "Too many sign-in attempts. Try again in 15 minutes.")
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if decoder.Decode(&body) != nil || body.Password == "" || len(body.Password) > 1024 || decoder.Decode(new(any)) != io.EOF {
			failure(w, 400, "Send a valid password.")
			return
		}
		matches, ready := h.sessions.passwordMatches(body.Password, h.config.PasswordHash)
		if !ready {
			failure(w, 503, "Sign-in is busy. Try again shortly.")
			return
		}
		if !matches {
			failure(w, 401, "Password was not accepted.")
			return
		}
		if !h.issueSession(w, r) {
			return
		}
		jsonResponse(w, 200, map[string]any{"authenticated": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (h *Handler) issueSession(w http.ResponseWriter, r *http.Request) bool {
	token, v := h.sessions.create(peerAddress(r))
	if v == nil {
		failure(w, 503, "The session limit was reached. Try again later.")
		return false
	}
	h.revoke(w, r)
	h.setCookie(w, "session", token, int(sessionLifetime.Seconds()))
	context.AfterFunc(v.ctx, func() { h.forgetDevice(v) })
	return true
}

func (h *Handler) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if strings.Contains(name, "\\") || path.Clean("/"+name) != strings.TrimSuffix("/"+name, "/") && name != "" || strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	if name == "" {
		name = "index.html"
	}
	file, err := h.files.Open(name)
	if err == nil {
		info, errStat := file.Stat()
		if errClose := file.Close(); errClose != nil {
			log.Debug("console: static file close failed")
		}
		if errStat != nil || info.IsDir() {
			err = fs.ErrNotExist
		}
	}
	if err != nil {
		// Missing assets must be 404s, never HTML with an immutable cache lifetime.
		if strings.HasPrefix(name, "_app/") || path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
	}
	if strings.HasPrefix(name, "_app/immutable/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, h.files, name)
}

// publishMutation preserves the console's live invalidation semantics.
func publishMutation() {
	managementevents.Publish(managementevents.Config | managementevents.Accounts | managementevents.DeviceAuth)
}
