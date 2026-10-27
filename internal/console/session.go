package console

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/scrypt"
)

const sessionLifetime = 8 * time.Hour
const maxEntries = 1000

type session struct {
	expires time.Time
	ctx     context.Context
	cancel  context.CancelFunc
	// Serialize device operations, including cancellation on logout.
	deviceMu     sync.Mutex
	deviceState  string
	deviceResult map[string]any
}
type attempt struct {
	count   int
	expires time.Time
}
type transaction struct {
	state, nonce, verifier string
	expires                time.Time
}

type sessions struct {
	mu           sync.Mutex
	entries      map[string]*session
	attempts     map[string]attempt
	transactions map[string]transaction
	now          func() time.Time
	verifying    chan struct{}
}

func newSessions() *sessions {
	return &sessions{entries: make(map[string]*session), attempts: make(map[string]attempt), transactions: make(map[string]transaction), now: time.Now, verifying: make(chan struct{}, 2)}
}
func randomToken() string {
	sum := sha256.Sum256([]byte(rand.Text()))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func digest(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

func (s *sessions) pruneLocked() {
	now := s.now()
	for key, v := range s.entries {
		if !now.Before(v.expires) {
			v.cancel()
			delete(s.entries, key)
		}
	}
	for key, v := range s.attempts {
		if !now.Before(v.expires) {
			delete(s.attempts, key)
		}
	}
	for key, v := range s.transactions {
		if !now.Before(v.expires) {
			delete(s.transactions, key)
		}
	}
}
func (s *sessions) permit(address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	key := digest(address)
	a, exists := s.attempts[key]
	if a.count >= 5 || (!exists && len(s.attempts) >= maxEntries) {
		return false
	}
	if !exists {
		a.expires = s.now().Add(15 * time.Minute)
	}
	a.count++
	s.attempts[key] = a
	return true
}
func (s *sessions) create(address string) (string, *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if len(s.entries) >= maxEntries {
		return "", nil
	}
	token := randomToken()
	ctx, cancel := context.WithCancel(context.Background())
	v := &session{expires: s.now().Add(sessionLifetime), ctx: ctx, cancel: cancel}
	timer := time.AfterFunc(sessionLifetime, cancel)
	context.AfterFunc(ctx, func() { timer.Stop() })
	s.entries[digest(token)] = v
	delete(s.attempts, digest(address))
	return token, v
}
func (s *sessions) get(token string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if len(token) != 43 {
		return nil
	}
	return s.entries[digest(token)]
}
func (s *sessions) revoke(token string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.entries[digest(token)]
	delete(s.entries, digest(token))
	if v != nil {
		v.cancel()
	}
	return v
}
func (s *sessions) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, v := range s.entries {
		v.cancel()
		delete(s.entries, key)
	}
	clear(s.transactions)
}
func (s *sessions) passwordMatches(password, encoded string) (bool, bool) {
	select {
	case s.verifying <- struct{}{}:
		defer func() { <-s.verifying }()
	default:
		return false, false
	}
	parts := passwordPattern.FindStringSubmatch(encoded)
	if parts == nil {
		return false, true
	}
	salt, _ := hex.DecodeString(parts[1])
	expected, _ := hex.DecodeString(parts[2])
	actual, err := scrypt.Key([]byte(password), salt, 131072, 8, 1, 64)
	return err == nil && subtle.ConstantTimeCompare(actual, expected) == 1, true
}
func peerAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
func (h *Handler) cookieName(suffix string) string {
	if strings.HasPrefix(h.config.Origin, "https://") {
		return "__Host-management_" + suffix
	}
	return "management_" + suffix
}
func (h *Handler) setCookie(w http.ResponseWriter, suffix, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: h.cookieName(suffix), Value: value, Path: "/", MaxAge: age, Secure: strings.HasPrefix(h.config.Origin, "https://"), HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
func (h *Handler) cookie(r *http.Request, suffix string) string {
	c, err := r.Cookie(h.cookieName(suffix))
	if err != nil {
		return ""
	}
	return c.Value
}
func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	h.sessions.revoke(h.cookie(r, "session"))
	h.consumeTransaction(h.cookie(r, "oidc"))
	h.setCookie(w, "oidc", "", -1)
	h.setCookie(w, "session", "", -1)
}
