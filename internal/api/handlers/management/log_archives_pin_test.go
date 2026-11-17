package management

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/logarchive"
)

// Pins request log name classification, including Unicode and whitespace handling.
func TestRequestLogKindPinnedCases(t *testing.T) {
	for name, want := range map[string]string{
		"v1-responses-2026-10-27T120000-abcd1234.log":       "request",
		"error-v1-responses-2026-10-27T120000-abcd1234.log": "error",
		"ünïcode-2026-10-27T120000-a.b_c-d.log":             "request",
		"nbsp\u00a0name-2026-10-27T120000-a.log":            "request",
		"tab\tname-2026-10-27T120000-a.log":                 "",
		"space name-2026-10-27T120000-a.log":                "",
		"v1-2026-13-27T120000-a.log":                        "",
		"v1-2026-10-27T120000-ü.log":                        "",
		"v1-2026-10-27T120000-a.log.gz":                     "",
		"v1-٢٠٢٦-10-27T120000-a.log":                        "",
		"a<b-2026-10-27T120000-a.log":                       "",
		"main.log":                                          "",
	} {
		kind, ok := requestLogKind(name)
		if kind != want || ok != (want != "") {
			t.Errorf("requestLogKind(%q) = %q, %v; want %q", name, kind, ok, want)
		}
	}
}

// Pins the cursor wire format: unpadded URL-safe Base64 of compact JSON.
func TestLogArchiveCursorPinnedEncoding(t *testing.T) {
	for _, tc := range []struct{ source, after, want string }{
		{"local", "", "eyJzb3VyY2UiOiJsb2NhbCIsImFmdGVyIjoiIn0"},
		{"s3", "logs/a<b>&?", "eyJzb3VyY2UiOiJzMyIsImFmdGVyIjoibG9ncy9hXHUwMDNjYlx1MDAzZVx1MDAyNj8ifQ"},
		{"s3", "ü", "eyJzb3VyY2UiOiJzMyIsImFmdGVyIjoiw7wifQ"},
	} {
		if got := encodeLogArchiveCursor(tc.source, tc.after); got != tc.want {
			t.Errorf("encodeLogArchiveCursor(%q, %q) = %q, want %q", tc.source, tc.after, got, tc.want)
		}
	}
}

// Pins which cursors the endpoint accepts: padding, alphabet, key case and unknown fields.
func TestLogArchiveCursorPinnedDecoding(t *testing.T) {
	h := archiveTestHandler(t, true, archiveReaderStub{list: func(context.Context, string, int) (logarchive.ListResult, error) {
		return logarchive.ListResult{}, nil
	}})
	for cursor, want := range map[string]int{
		"eyJzb3VyY2UiOiJzMyIsImFmdGVyIjoiIn0":      http.StatusOK,         // {"source":"s3","after":""}
		"eyJzb3VyY2UiOiJzMyIsImFmdGVyIjoiIn0=":     http.StatusBadRequest, // padded
		"eyJTT1VSQ0UiOiJzMyJ9":                     http.StatusOK,         // {"SOURCE":"s3"}
		"eyJzb3VyY2UiOiJzMyIsImV4dHJhIjoxfQ":       http.StatusOK,         // unknown field
		"eyJzb3VyY2UiOiJzMyJ9IA":                   http.StatusOK,         // trailing space
		"eyJzb3VyY2UiOiJzMyJ9e30":                  http.StatusBadRequest, // trailing object
		"eyJzb3VyY2UiOjN9":                         http.StatusBadRequest, // wrong type
		"eyJzb3VyY2UiOiJzMyIsImFmdGVyIjoiYS9iIn0":  http.StatusOK,         // {"source":"s3","after":"a/b"}
		"eyJzb3VyY2UiOiJzMyIsImFmdGVyIjoiYS9iIn0+": http.StatusBadRequest, // standard alphabet byte
	} {
		response := archiveRequest(h.GetLogArchives, "/?cursor="+url.QueryEscape(cursor))
		if response.Code != want {
			t.Errorf("cursor %q: status %d, want %d: %s", cursor, response.Code, want, response.Body.String())
		}
	}
}
