package console

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

func (h *Handler) consumeTransaction(token string) (transaction, bool) {
	h.sessions.mu.Lock()
	defer h.sessions.mu.Unlock()
	h.sessions.pruneLocked()
	key := digest(token)
	v, ok := h.sessions.transactions[key]
	delete(h.sessions.transactions, key)
	return v, ok
}
func (h *Handler) oidcProvider(ctx context.Context) (*oidc.Provider, error) {
	h.providerMu.Lock()
	defer h.providerMu.Unlock()
	if h.provider != nil {
		return h.provider, nil
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, h.authClient), h.config.Issuer)
	if err == nil {
		h.provider = provider
	}
	return provider, err
}
func (h *Handler) oauthConfig(provider *oidc.Provider) oauth2.Config {
	endpoint := provider.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInHeader
	return oauth2.Config{ClientID: h.config.ClientID, ClientSecret: h.config.ClientSecret, Endpoint: endpoint, RedirectURL: h.config.Origin + "/auth/oidc/callback", Scopes: []string{oidc.ScopeOpenID, "profile"}}
}
func oidcRedirect(w http.ResponseWriter, r *http.Request, location string) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, location, 303)
}
func (h *Handler) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if h.config.Issuer == "" {
		failure(w, 503, "Authelia sign-in is not configured.")
		return
	}
	if !h.sessions.permit(peerAddress(r)) {
		failure(w, 429, "Too many sign-in attempts. Try again in 15 minutes.")
		return
	}
	h.consumeTransaction(h.cookie(r, "oidc"))
	h.setCookie(w, "oidc", "", -1)
	provider, err := h.oidcProvider(r.Context())
	if err != nil {
		oidcRedirect(w, r, "/login?error=oidc")
		return
	}
	token := randomToken()
	tx := transaction{state: randomToken(), nonce: randomToken(), verifier: randomToken(), expires: h.sessions.now().Add(10 * time.Minute)}
	h.sessions.mu.Lock()
	h.sessions.pruneLocked()
	if len(h.sessions.transactions) >= maxEntries {
		h.sessions.mu.Unlock()
		failure(w, 503, "Sign-in is temporarily unavailable.")
		return
	}
	h.sessions.transactions[digest(token)] = tx
	h.sessions.mu.Unlock()
	h.setCookie(w, "oidc", token, 600)
	challenge := sha256.Sum256([]byte(tx.verifier))
	oauth := h.oauthConfig(provider)
	location := oauth.AuthCodeURL(tx.state, oauth2.SetAuthURLParam("nonce", tx.nonce), oauth2.SetAuthURLParam("response_mode", "query"), oauth2.SetAuthURLParam("code_challenge_method", "S256"), oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:])))
	oidcRedirect(w, r, location)
}
func (h *Handler) oidcCallback(w http.ResponseWriter, r *http.Request) {
	tx, ok := h.consumeTransaction(h.cookie(r, "oidc"))
	h.setCookie(w, "oidc", "", -1)
	query, errQuery := url.ParseQuery(r.URL.RawQuery)
	valid := ok && errQuery == nil && len(query["state"]) == 1 && len(query["code"]) == 1 && query.Get("state") == tx.state && query.Get("code") != "" && !query.Has("error")
	if !valid || h.config.Issuer == "" || !h.authorized(r.Context(), query.Get("code"), tx) {
		oidcRedirect(w, r, "/login?error=oidc")
		return
	}
	if h.issueSession(w, r) {
		oidcRedirect(w, r, "/")
	}
}
func (h *Handler) authorized(ctx context.Context, code string, tx transaction) bool {
	provider, err := h.oidcProvider(ctx)
	if err != nil {
		return false
	}
	ctx = oidc.ClientContext(ctx, h.authClient)
	oauth := h.oauthConfig(provider)
	tokens, err := oauth.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", tx.verifier))
	if err != nil {
		return false
	}
	raw, ok := tokens.Extra("id_token").(string)
	if !ok {
		return false
	}
	idToken, err := provider.VerifierContext(ctx, &oidc.Config{ClientID: h.config.ClientID, SupportedSigningAlgs: []string{oidc.RS256}}).Verify(ctx, raw)
	if err != nil || idToken.Subject == "" || idToken.Nonce != tx.nonce {
		return false
	}
	if idToken.AccessTokenHash != "" && idToken.VerifyAccessToken(tokens.AccessToken) != nil {
		return false
	}
	user, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(tokens))
	if err != nil || user.Subject != idToken.Subject {
		return false
	}
	var claims struct {
		Username string `json:"preferred_username"`
	}
	return user.Claims(&claims) == nil && slices.Contains(h.config.AllowedUsers, claims.Username)
}
