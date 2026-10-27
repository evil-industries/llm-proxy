package console

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

var methods = map[string][]string{
	"config": {"GET"}, "throughput": {"GET"}, "events": {"GET"},
	"auth-files": {"GET", "POST", "DELETE"}, "auth-files/status": {"PATCH"},
	"api-keys": {"GET", "PUT", "PATCH", "DELETE"}, "api-keys/mutate": {"POST"},
	"logs": {"GET"}, "request-logs": {"GET"}, "log-archives": {"GET"}, "log-archives/download": {"GET"},
	"notifications": {"GET", "PUT"}, "notifications/test": {"POST"}, "routing/strategy": {"GET", "PUT"},
	"debug": {"PUT"}, "logging-to-file": {"PUT"}, "usage-statistics-enabled": {"PUT"}, "request-log": {"PUT"},
	"ws-auth": {"PUT"}, "force-model-prefix": {"PUT"}, "quota-exceeded/switch-project": {"PUT"}, "quota-exceeded/switch-preview-model": {"PUT"},
}
var unsafeName = regexp.MustCompile(`[\\\x00-\x1f\x7f]`)
var digits = regexp.MustCompile(`^\d+$`)

func logFile(p string) (bool, bool) {
	parts := strings.Split(p, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] != "request-logs" || !strings.HasSuffix(parts[1], ".log") || unsafeName.MatchString(parts[1]) || strings.ContainsAny(parts[1], "?#") {
		return false, false
	}
	if len(parts) == 3 && parts[2] != "download" {
		return false, false
	}
	return true, len(parts) == 3
}
func validQuery(p, method string, q url.Values) bool {
	var allowed []string
	file, download := logFile(p)
	switch {
	case file:
		if !download {
			allowed = []string{"offset", "limit"}
		}
	case p == "log-archives":
		allowed = []string{"limit", "cursor"}
	case p == "log-archives/download":
		allowed = []string{"source", "key"}
	case p == "logs":
		allowed = []string{"limit", "cursor", "after"}
	case p == "auth-files" && method == "GET":
		allowed = []string{"name", "auth_index"}
	case p == "auth-files" && method == "DELETE":
		allowed = []string{"name"}
	case p == "api-keys" && method == "DELETE":
		allowed = []string{"index"}
	}
	for key, values := range q {
		if !slices.Contains(allowed, key) || len(values) != 1 {
			return false
		}
	}
	if p == "log-archives/download" {
		source, key := q.Get("source"), q.Get("key")
		if source != "local" && source != "s3" || key == "" || unsafeName.MatchString(key) {
			return false
		}
		parts := strings.Split(key, "/")
		if source == "local" && len(parts) != 1 {
			return false
		}
		for _, part := range parts {
			if part == "" || part == "." || part == ".." {
				return false
			}
		}
	}
	if p == "log-archives" && q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 1000 {
			return false
		}
	}
	if p == "auth-files" && method == "DELETE" {
		return q.Get("name") != ""
	}
	if p == "api-keys" && method == "DELETE" {
		return digits.MatchString(q.Get("index"))
	}
	return true
}

func (h *Handler) management(w http.ResponseWriter, r *http.Request, v *session) {
	p := strings.TrimPrefix(r.URL.Path, "/api/management/")
	file, download := logFile(p)
	if !(file && r.Method == "GET") && !slices.Contains(methods[p], r.Method) {
		failure(w, 404, "Management endpoint is not available.")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !validQuery(p, r.Method, query) {
		failure(w, 400, "Invalid management query.")
		return
	}
	if slices.Contains([]string{"POST", "PUT", "PATCH"}, r.Method) {
		media, params, errType := mime.ParseMediaType(r.Header.Get("Content-Type"))
		multipart := p == "auth-files" && r.Method == "POST" && media == "multipart/form-data" && params["boundary"] != ""
		if errType != nil || !multipart && media != "application/json" {
			failure(w, 415, "Use JSON or a credential file upload.")
			return
		}
	}
	// Never forward browser cookies, bearer credentials, proxy headers or arbitrary URLs.
	upstream := r.Clone(r.Context())
	upstream.URL = &url.URL{Path: "/" + p, RawQuery: r.URL.RawQuery}
	upstream.RequestURI = upstream.URL.RequestURI()
	upstream.Header = make(http.Header)
	upstream.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	upstream.Header.Set("Accept", "application/json")
	upstream.Host = "console.internal"
	upstream.RemoteAddr = "127.0.0.1:0"
	if p == "auth-files" && r.Method == "GET" {
		// The legacy admin endpoint includes paths, token claims and extension metadata.
		// Only account presentation fields belong in the public console API.
		result := newBufferedResponse()
		h.backend.ServeHTTP(result, upstream)
		if result.Code >= 400 {
			upstreamFailure(w, result.Code)
			return
		}
		var data map[string]any
		if json.Unmarshal(result.Body.Bytes(), &data) != nil {
			failure(w, 502, "Invalid account response.")
			return
		}
		accounts := []any{}
		if entries, ok := data["files"].([]any); ok {
			for _, raw := range entries {
				if entry, okEntry := raw.(map[string]any); okEntry {
					accounts = append(accounts, publicAccount(entry))
				}
			}
		}
		jsonResponse(w, 200, map[string]any{"files": accounts, "observed_at": data["observed_at"]})
		return
	}
	writer := &safeWriter{ResponseWriter: w, headers: make(http.Header), ctx: r.Context()}
	if file && download || p == "log-archives/download" {
		name := path.Base(query.Get("key"))
		if file {
			name = strings.Split(p, "/")[1]
		}
		contentType := "text/plain; charset=utf-8"
		if strings.HasSuffix(name, ".gz") {
			contentType = "application/gzip"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	}
	h.backend.ServeHTTP(writer, upstream)
	if p == "events" && v.ctx.Err() != nil && writer.status < 400 {
		if _, errWrite := fmt.Fprint(w, "event: session-ended\ndata: {}\n\n"); errWrite != nil {
			log.Debug("console: event stream closed")
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	if r.Method != "GET" && writer.status < 400 {
		publishMutation()
	}
}
func publicAccount(entry map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"id", "auth_index", "name", "type", "provider", "label", "email", "status", "status_message", "disabled", "unavailable", "runtime_only", "source", "size", "success", "failed", "recent_requests", "quota", "model_quotas", "cooldowns", "account_type", "created_at", "modtime", "updated_at", "last_refresh", "next_retry_after"} {
		if value, ok := entry[key]; ok {
			result[key] = value
		}
	}
	if claims, ok := entry["id_token"].(map[string]any); ok {
		if plan, okPlan := claims["plan_type"].(string); okPlan {
			result["id_token"] = map[string]any{"plan_type": plan}
		}
	}
	return result
}
func upstreamFailure(w http.ResponseWriter, status int) {
	if status == 401 || status == 403 || status >= 500 {
		status = 502
	}
	failure(w, status, "The proxy server could not complete this request.")
}

// safeWriter streams successful bodies and allowlisted headers. Error details and
// server-only headers never cross the console boundary. It supports SSE flushing.
type safeWriter struct {
	http.ResponseWriter
	headers http.Header
	status  int
	ctx     context.Context
}

func (w *safeWriter) Header() http.Header { return w.headers }
func (w *safeWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status >= 400 {
		upstreamFailure(w.ResponseWriter, status)
		return
	}
	for _, key := range []string{"Content-Type", "Content-Range", "Accept-Ranges", "X-Accel-Buffering"} {
		if value := w.headers.Get(key); value != "" && w.ResponseWriter.Header().Get(key) == "" {
			w.ResponseWriter.Header().Set(key, value)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *safeWriter) Write(b []byte) (int, error) {
	if w.ctx != nil && w.ctx.Err() != nil {
		return 0, w.ctx.Err()
	}
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if w.status >= 400 {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}
func (w *safeWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (h *Handler) deviceCall(r *http.Request, method, state string) (map[string]any, int) {
	request := r.Clone(r.Context())
	request.Method = method
	request.URL = &url.URL{Path: "/codex/device-auth"}
	if state != "" {
		request.URL.RawQuery = url.Values{"state": {state}}.Encode()
	}
	request.Header = make(http.Header)
	request.Header.Set("Content-Type", "application/json")
	request.Body = http.NoBody
	if method == "POST" {
		request.Body = io.NopCloser(strings.NewReader("{}"))
	}
	request.RequestURI = request.URL.RequestURI()
	recorder := newBufferedResponse()
	h.backend.ServeHTTP(recorder, request)
	var data map[string]any
	if recorder.Code >= 400 {
		return nil, recorder.Code
	}
	if json.Unmarshal(recorder.Body.Bytes(), &data) != nil {
		return nil, 502
	}
	return data, recorder.Code
}

// bufferedResponse is only used for small, explicitly projected account/device
// metadata. Uploads, log downloads, inference and events never pass through it.
type bufferedResponse struct {
	headers http.Header
	Body    bytes.Buffer
	Code    int
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{headers: make(http.Header), Code: 200}
}
func (w *bufferedResponse) Header() http.Header         { return w.headers }
func (w *bufferedResponse) WriteHeader(code int)        { w.Code = code }
func (w *bufferedResponse) Write(b []byte) (int, error) { return w.Body.Write(b) }
