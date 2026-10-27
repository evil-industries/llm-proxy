package console

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// This fixture exercises discovery, client_secret_basic, PKCE, signed identity
// tokens and UserInfo through actual TLS HTTP requests, without live accounts.
func TestOIDCAuthenticationAndDeniedAccess(t *testing.T) {
	for _, scenario := range []string{"success", "unlisted user", "wrong subject", "wrong issuer", "wrong audience", "expired token", "wrong nonce", "bad signature", "missing cookie", "wrong state", "expired transaction", "duplicate code", "provider error"} {
		t.Run(scenario, func(t *testing.T) {
			h := fixture(t, nil)
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
			if err != nil {
				t.Fatal(err)
			}
			var issuer string
			var tx transaction
			var exchanges int
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					jsonResponse(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "userinfo_endpoint": issuer + "/userinfo", "jwks_uri": issuer + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
				case "/jwks":
					jsonResponse(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, Algorithm: "RS256", Use: "sig"}}})
				case "/token":
					exchanges++
					user, password, ok := r.BasicAuth()
					if !ok || user != "test-console" || password != "private-client-secret" {
						t.Error("client_secret_basic missing")
					}
					if errForm := r.ParseForm(); errForm != nil {
						t.Error(errForm)
					}
					if r.Form.Get("code_verifier") != tx.verifier || r.Form.Get("redirect_uri") != "https://console.example/auth/oidc/callback" || r.Form.Get("code") != "test-code" {
						t.Errorf("bad code exchange: %v", r.Form)
					}
					now := time.Now()
					claims := map[string]any{"iss": issuer, "sub": "person", "aud": "test-console", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": tx.nonce}
					switch scenario {
					case "wrong issuer":
						claims["iss"] = "https://evil.example"
					case "wrong audience":
						claims["aud"] = "other-client"
					case "expired token":
						claims["exp"] = now.Add(-time.Hour).Unix()
					case "wrong nonce":
						claims["nonce"] = "other-browser"
					}
					raw, _ := json.Marshal(claims)
					signed, errSign := signer.Sign(raw)
					if errSign != nil {
						t.Error(errSign)
						w.WriteHeader(500)
						return
					}
					token, errCompact := signed.CompactSerialize()
					if errCompact != nil {
						t.Error(errCompact)
						w.WriteHeader(500)
						return
					}
					if scenario == "bad signature" {
						parts := strings.Split(token, ".")
						parts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, 256))
						token = strings.Join(parts, ".")
					}
					jsonResponse(w, 200, map[string]any{"access_token": "private-access-token", "token_type": "Bearer", "id_token": token, "expires_in": 3600})
				case "/userinfo":
					if r.Header.Get("Authorization") != "Bearer private-access-token" {
						t.Error("missing UserInfo bearer token")
					}
					subject, username := "person", "alice"
					if scenario == "wrong subject" {
						subject = "someone-else"
					}
					if scenario == "unlisted user" {
						username = "mallory"
					}
					jsonResponse(w, 200, map[string]any{"sub": subject, "preferred_username": username})
				default:
					http.NotFound(w, r)
				}
			}))
			defer provider.Close()
			issuer = provider.URL
			h.config.Issuer = issuer
			h.config.ClientID = "test-console"
			h.config.ClientSecret = "private-client-secret"
			h.config.AllowedUsers = []string{"alice"}
			h.authClient = provider.Client()
			// OIDC mode never falls back to the configured password hash.
			checkStatus(t, request(h, "POST", "/api/session", "", `{"password":"a-long-test-password"}`), 403)
			start := request(h, "POST", "/auth/oidc/login", "", "")
			checkStatus(t, start, 303)
			if start.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("callback referrer protection missing")
			}
			location, errURL := url.Parse(start.Header().Get("Location"))
			if errURL != nil {
				t.Fatal(errURL)
			}
			var cookie *http.Cookie
			for _, c := range start.Result().Cookies() {
				if c.Name == "__Host-management_oidc" && c.MaxAge > 0 {
					cookie = c
				}
			}
			if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.MaxAge != 600 {
				t.Fatalf("transaction cookie: %+v", cookie)
			}
			tx = h.sessions.transactions[digest(cookie.Value)]
			challenge := sha256.Sum256([]byte(tx.verifier))
			q := location.Query()
			if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(challenge[:]) || q.Get("code_challenge_method") != "S256" || q.Get("state") != tx.state || q.Get("nonce") != tx.nonce {
				t.Fatal("OIDC binding missing")
			}
			if strings.Contains(location.String(), "private-client-secret") || strings.Contains(start.Body.String(), tx.verifier) {
				t.Fatal("OIDC secret exposed")
			}
			callback := "/auth/oidc/callback?code=test-code&state=" + url.QueryEscape(tx.state)
			switch scenario {
			case "wrong state":
				callback = "/auth/oidc/callback?code=test-code&state=other"
			case "duplicate code":
				callback += "&code=other"
			case "provider error":
				callback += "&error=access_denied"
			case "expired transaction":
				h.sessions.now = func() time.Time { return tx.expires }
			}
			req := httptest.NewRequest("GET", "https://console.example"+callback, nil)
			if scenario != "missing cookie" {
				req.AddCookie(cookie)
			}
			req.RemoteAddr = "192.0.2.1:1234"
			result := httptest.NewRecorder()
			h.ServeHTTP(result, req)
			checkStatus(t, result, 303)
			var sessionCookie *http.Cookie
			for _, c := range result.Result().Cookies() {
				if c.Name == "__Host-management_session" && c.MaxAge > 0 {
					sessionCookie = c
				}
			}
			if scenario == "success" {
				if result.Header().Get("Location") != "/" || sessionCookie == nil || h.sessions.get(sessionCookie.Value) == nil {
					t.Fatalf("OIDC success did not create session: %v", result.Header())
				}
				checkStatus(t, request(h, "DELETE", "/api/session", sessionCookie.Value, ""), 200)
				if h.sessions.get(sessionCookie.Value) != nil {
					t.Fatal("OIDC logout did not revoke")
				}
			} else if result.Header().Get("Location") != "/login?error=oidc" || sessionCookie != nil {
				t.Fatalf("denied OIDC succeeded: %v", result.Header())
			}
			// A callback is one-use even if authorization failed.
			before := exchanges
			replay := httptest.NewRecorder()
			h.ServeHTTP(replay, req.Clone(req.Context()))
			if replay.Header().Get("Location") != "/login?error=oidc" || exchanges != before {
				t.Fatal("OIDC callback replayed")
			}
			for _, secret := range []string{"private-client-secret", "private-access-token", tx.verifier, tx.nonce} {
				if strings.Contains(result.Body.String(), secret) {
					t.Fatal("secret in callback response")
				}
			}
		})
	}
}

func TestOIDCCSRFAndTransactionBound(t *testing.T) {
	h := fixture(t, nil)
	h.config.Issuer = "https://unused.example"
	req := httptest.NewRequest("POST", "https://console.example/auth/oidc/login", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	checkStatus(t, w, 403)
	// Deleting a session consumes an outstanding browser-bound transaction too.
	cookie := randomToken()
	h.sessions.transactions[digest(cookie)] = transaction{state: "state", expires: time.Now().Add(time.Minute)}
	req = httptest.NewRequest("DELETE", "https://console.example/api/session", nil)
	req.Header.Set("Origin", h.config.Origin)
	req.AddCookie(&http.Cookie{Name: "__Host-management_oidc", Value: cookie})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	checkStatus(t, w, 200)
	if _, exists := h.sessions.transactions[digest(cookie)]; exists {
		t.Fatal("logout retained OIDC transaction")
	}
}
