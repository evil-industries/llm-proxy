package console

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeForge/moego/base64"
)

// Pins the console's input patterns: anchors, ASCII classes and trailing newlines.
func TestConsolePatternsPinned(t *testing.T) {
	hash := "scrypt$131072$8$1$" + strings.Repeat("a", 32) + "$" + strings.Repeat("0", 128)
	for input, want := range map[string]bool{
		hash:                                   true,
		hash + "\n":                            false,
		strings.ToUpper(hash):                  false,
		strings.Replace(hash, "a", "A", 1):     false,
		"x" + hash:                             false,
		strings.Replace(hash, "$8$", "$9$", 1): false,
	} {
		if got := passwordPattern.MatchString(input); got != want {
			t.Errorf("passwordPattern(%q) = %v, want %v", input, got, want)
		}
	}
	for input, want := range map[string]bool{
		strings.Repeat("a", 16):        true,
		strings.Repeat("Z9_-", 64):     true,
		strings.Repeat("a", 15):        false,
		strings.Repeat("a", 257):       false,
		strings.Repeat("a", 16) + "\n": false,
		strings.Repeat("é", 16):        false,
		strings.Repeat("a", 15) + "=":  false,
	} {
		if got := statePattern.MatchString(input); got != want {
			t.Errorf("statePattern(%q) = %v, want %v", input, got, want)
		}
	}
	for input, want := range map[string]bool{
		"plain.log":     false,
		"ü-name.log":    false,
		"a\\b.log":      true,
		"a\x00b":        true,
		"tab\tname":     true,
		"del\x7f":       true,
		"c1\u0085name":  false,
		"line\u2028sep": false,
	} {
		if got := unsafeName.MatchString(input); got != want {
			t.Errorf("unsafeName(%q) = %v, want %v", input, got, want)
		}
	}
	for input, want := range map[string]bool{
		"0":    true,
		"0123": true,
		"":     false,
		"1a":   false,
		"-1":   false,
		"١٢":   false,
		"1\n":  false,
	} {
		if got := digits.MatchString(input); got != want {
			t.Errorf("digits(%q) = %v, want %v", input, got, want)
		}
	}
}

// Pins JSON responses: HTML escaping, sorted map keys and the trailing newline.
func TestJSONResponsePinned(t *testing.T) {
	w := httptest.NewRecorder()
	jsonResponse(w, 201, map[string]any{"z": 1.5, "error": "<a&b>", "a": []any{nil, true, "\u2028"}})
	want := `{"a":[null,true,"\u2028"],"error":"\u003ca\u0026b\u003e","z":1.5}` + "\n"
	if w.Code != 201 || w.Body.String() != want || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("jsonResponse = %d %q, want %q", w.Code, w.Body.String(), want)
	}
}

// Pins session and PKCE encodings: unpadded URL-safe Base64.
func TestTokenEncodingPinned(t *testing.T) {
	for i := 0; i < 32; i++ {
		token := randomToken()
		if len(token) != 43 || strings.Trim(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			t.Fatalf("randomToken() = %q", token)
		}
	}
	// Value cross-checked with Python hashlib and urlsafe_b64encode.
	challenge := sha256.Sum256([]byte("dBjftJeZ4CVP-mJ92K9xYyRmGXb6ONmSwWOGxsdnwM"))
	if got := base64.RawURLEncoding.EncodeToString(challenge[:]); got != "nwrK1oZ2sL3Hl_CmoQNF4k4tON9oyjiWAKLGQMhxEv8" {
		t.Fatalf("S256 challenge = %q", got)
	}
}

// Pins the account projection: decoded numbers are float64 and re-encoded as such.
func TestAccountProjectionNumbersPinned(t *testing.T) {
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"observed_at":"2026-11-17T00:00:00Z","files":[{"id":"a<b>","size":9007199254740993,"success":1.50,"failed":1e3,"quota":{"used":0.1,"limit":-0},"disabled":false,"access_token":"private","id_token":{"plan_type":"pro","email":"x"}}]}`))
	})
	h := fixture(t, backend)
	w := request(h, "GET", "/api/management/auth-files", newToken(t, h), "")
	checkStatus(t, w, 200)
	want := `{"files":[{"disabled":false,"failed":1000,"id":"a\u003cb\u003e","id_token":{"plan_type":"pro"},"quota":{"limit":-0,"used":0.1},"size":9007199254740992,"success":1.5}],"observed_at":"2026-11-17T00:00:00Z"}` + "\n"
	if w.Body.String() != want {
		t.Fatalf("projection = %q\nwant         %q", w.Body.String(), want)
	}
}
