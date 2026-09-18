package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	codexLoginModeMetadataKey = "codex_login_mode"
	codexLoginModeDevice      = "device"
)

func shouldUseCodexDeviceFlow(opts *LoginOptions) bool {
	return opts != nil && opts.Metadata != nil && strings.EqualFold(strings.TrimSpace(opts.Metadata[codexLoginModeMetadataKey]), codexLoginModeDevice)
}

func (a *CodexAuthenticator) loginWithDeviceFlow(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	device := codex.NewDeviceAuth(cfg)
	challenge, err := device.Start(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Println("Starting Codex device authentication...")
	fmt.Printf("Codex device URL: %s\n", challenge.VerificationURI)
	fmt.Printf("Codex device code: %s\n", challenge.UserCode)
	if opts != nil && !opts.NoBrowser {
		if !browser.IsAvailable() {
			log.Warn("No browser available; please open the device URL manually")
		} else if errOpen := browser.OpenURL(challenge.VerificationURI); errOpen != nil {
			log.Warnf("Failed to open browser automatically: %v", errOpen)
		}
	}
	bundle, err := device.Complete(ctx, challenge, nil)
	if err != nil {
		return nil, err
	}
	return a.buildAuthRecord(codex.NewCodexAuth(cfg), bundle)
}

func (a *CodexAuthenticator) buildAuthRecord(authSvc *codex.CodexAuth, authBundle *codex.CodexAuthBundle) (*coreauth.Auth, error) {
	tokenStorage := authSvc.CreateTokenStorage(authBundle)

	if tokenStorage == nil || tokenStorage.Email == "" {
		return nil, fmt.Errorf("codex token storage missing account information")
	}

	planType := ""
	hashAccountID := ""
	if tokenStorage.IDToken != "" {
		if claims, errParse := codex.ParseJWTToken(tokenStorage.IDToken); errParse == nil && claims != nil {
			planType = strings.TrimSpace(claims.CodexAuthInfo.ChatgptPlanType)
			accountID := strings.TrimSpace(claims.CodexAuthInfo.ChatgptAccountID)
			if accountID != "" {
				digest := sha256.Sum256([]byte(accountID))
				hashAccountID = hex.EncodeToString(digest[:])[:8]
			}
		}
	}

	fileName := codex.CredentialFileName(tokenStorage.Email, planType, hashAccountID, true)
	metadata := map[string]any{
		"email": tokenStorage.Email,
	}

	fmt.Println("Codex authentication successful")
	if authBundle.APIKey != "" {
		fmt.Println("Codex API key obtained and stored")
	}

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Storage:  tokenStorage,
		Metadata: metadata,
		Attributes: map[string]string{
			"plan_type": planType,
		},
	}, nil
}
