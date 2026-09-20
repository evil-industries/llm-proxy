package management

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementevents"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const deviceTerminalRetention = 30 * time.Minute

type codexDeviceService interface {
	Start(context.Context) (*codex.DeviceChallenge, error)
	Complete(context.Context, *codex.DeviceChallenge, func(time.Duration)) (*codex.CodexAuthBundle, error)
}

type codexDeviceAccount struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	PlanType string `json:"plan_type"`
}

type codexDeviceStatus struct {
	State           string              `json:"state"`
	Status          string              `json:"status"`
	VerificationURI string              `json:"verification_uri"`
	UserCode        string              `json:"user_code"`
	ExpiresAt       time.Time           `json:"expires_at"`
	Interval        float64             `json:"interval"`
	Account         *codexDeviceAccount `json:"account,omitempty"`
	Error           string              `json:"error,omitempty"`
	Code            string              `json:"code,omitempty"`
}

type codexDeviceFlow struct {
	mu     sync.Mutex
	public codexDeviceStatus
	cancel context.CancelFunc
	done   chan struct{}
}

func (h *Handler) StartCodexDeviceAuth(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var body map[string]any
	if c.ShouldBindJSON(&body) != nil || body == nil || len(body) != 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Expected an empty JSON object.", "code": "invalid_request"})
		return
	}
	cfg := h.configSnapshot()
	if cfg == nil || h.authManagerSnapshot() == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Authentication storage is unavailable.", "code": "unavailable"})
		return
	}
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to start device authentication.", "code": "start_failed"})
		return
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	startCtx, cancelStart := context.WithCancel(c.Request.Context())
	h.deviceFlowsMu.Lock()
	if h.deviceAuthClosed {
		h.deviceFlowsMu.Unlock()
		cancelStart()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Gateway authentication is shutting down.", "code": "unavailable"})
		return
	}
	if h.deviceAuthStarts == nil {
		h.deviceAuthStarts = make(map[string]context.CancelFunc)
	}
	h.deviceAuthStarts[state] = cancelStart
	h.deviceFlowsMu.Unlock()
	defer func() {
		cancelStart()
		h.deviceFlowsMu.Lock()
		delete(h.deviceAuthStarts, state)
		h.deviceFlowsMu.Unlock()
	}()
	service := codexDeviceService(codex.NewDeviceAuth(cfg))
	if h.deviceAuthFactory != nil {
		service = h.deviceAuthFactory(cfg)
	}
	challenge, err := service.Start(startCtx)
	if err != nil || challenge == nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Unable to start Codex device authentication. Try again.", "code": "start_failed"})
		return
	}
	flowCtx, cancel := context.WithDeadline(context.WithoutCancel(c.Request.Context()), challenge.ExpiresAt)
	flow := &codexDeviceFlow{
		public: codexDeviceStatus{State: state, Status: "pending", VerificationURI: challenge.VerificationURI, UserCode: challenge.UserCode, ExpiresAt: challenge.ExpiresAt.UTC(), Interval: challenge.Interval.Seconds()},
		cancel: cancel, done: make(chan struct{}),
	}
	initialResponse := flow.public
	h.deviceFlowsMu.Lock()
	if h.deviceAuthClosed {
		h.deviceFlowsMu.Unlock()
		cancel()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Gateway authentication is shutting down.", "code": "unavailable"})
		return
	}
	if h.deviceFlows == nil {
		h.deviceFlows = make(map[string]*codexDeviceFlow)
	}
	h.deviceFlows[state] = flow
	if h.deviceAuthWorkers == nil {
		h.deviceAuthWorkers = make(map[*codexDeviceFlow]struct{})
	}
	h.deviceAuthWorkers[flow] = struct{}{}
	h.deviceFlowsMu.Unlock()
	// Retain terminal results for reconnecting browsers without keeping flows forever.
	time.AfterFunc(time.Until(challenge.ExpiresAt.Add(deviceTerminalRetention)), func() {
		cancel()
		h.deviceFlowsMu.Lock()
		delete(h.deviceFlows, state)
		h.deviceFlowsMu.Unlock()
	})
	c.JSON(http.StatusOK, initialResponse)
	go h.runCodexDeviceAuth(flowCtx, flow, service, challenge)
}

func (h *Handler) runCodexDeviceAuth(ctx context.Context, flow *codexDeviceFlow, service codexDeviceService, challenge *codex.DeviceChallenge) {
	defer func() {
		h.deviceFlowsMu.Lock()
		delete(h.deviceAuthWorkers, flow)
		close(flow.done)
		h.deviceFlowsMu.Unlock()
	}()
	defer managementevents.Publish(managementevents.DeviceAuth)
	defer flow.cancel()
	bundle, err := service.Complete(ctx, challenge, func(interval time.Duration) {
		flow.mu.Lock()
		if flow.public.Status == "pending" {
			flow.public.Interval = interval.Seconds()
		}
		flow.mu.Unlock()
	})
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.public.Status != "pending" {
		return
	}
	if err != nil || ctx.Err() != nil || !time.Now().Before(flow.public.ExpiresAt) {
		if errors.Is(err, codex.ErrDeviceExpired) || errors.Is(ctx.Err(), context.DeadlineExceeded) || !time.Now().Before(flow.public.ExpiresAt) {
			flow.public.Status = "expired"
			flow.public.Code = "expired"
			flow.public.Error = "The device code expired. Start again to get a new code."
		} else if errors.Is(ctx.Err(), context.Canceled) {
			flow.public.Status = "cancelled"
		} else {
			flow.public.Status = "error"
			flow.public.Code = "authentication_failed"
			flow.public.Error = "Codex authentication failed. Start again and approve the code."
		}
		return
	}
	if bundle == nil || bundle.TokenData.AccessToken == "" || bundle.TokenData.RefreshToken == "" || bundle.TokenData.Email == "" {
		flow.public.Status, flow.public.Code, flow.public.Error = "error", "authentication_failed", "Codex returned incomplete account credentials. Start again."
		return
	}
	storage := codex.NewCodexAuth(nil).CreateTokenStorage(bundle)
	plan, accountHash := "", ""
	if claims, errParse := codex.ParseJWTToken(storage.IDToken); errParse == nil && claims != nil {
		plan = strings.TrimSpace(claims.CodexAuthInfo.ChatgptPlanType)
	}
	if storage.AccountID != "" {
		digest := sha256.Sum256([]byte(storage.AccountID))
		accountHash = hex.EncodeToString(digest[:])[:8]
	}
	name := codex.CredentialFileName(storage.Email, plan, accountHash, true)
	record := &coreauth.Auth{ID: name, FileName: name, Provider: "codex", Storage: storage,
		Metadata:   map[string]any{"email": storage.Email, "account_id": storage.AccountID},
		Attributes: map[string]string{"plan_type": plan},
	}
	// This is the commit point. Operator cancellation acquires the same flow mutex;
	// once persistence begins it observes completion/error instead of reporting a
	// cancellation that could still install credentials. Shutdown only cancels the
	// acquisition context; it must not wait for or interrupt this commit.
	persistCtx := context.WithoutCancel(ctx)
	savedPath, errSave := h.saveTokenRecord(persistCtx, record)
	if errSave == nil && strings.TrimSpace(savedPath) == "" {
		errSave = errors.New("credential was not persisted")
	}
	if errSave == nil {
		if h.authManagerSnapshot() == nil {
			errSave = errors.New("auth manager unavailable")
		} else {
			errSave = h.registerAuthFromFile(coreauth.WithSkipPersist(persistCtx), savedPath, nil)
		}
	}
	if errSave != nil {
		flow.public.Status, flow.public.Code, flow.public.Error = "error", "persistence_failed", "Unable to install the account credentials. Check gateway storage and try again."
		return
	}
	flow.public.Status = "complete"
	flow.public.Account = &codexDeviceAccount{Name: name, Email: storage.Email, PlanType: plan}
}

func (h *Handler) GetCodexDeviceAuth(c *gin.Context)    { h.codexDeviceStatus(c, false) }
func (h *Handler) CancelCodexDeviceAuth(c *gin.Context) { h.codexDeviceStatus(c, true) }

func (h *Handler) codexDeviceStatus(c *gin.Context, cancel bool) {
	c.Header("Cache-Control", "no-store")
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Device authentication state is required.", "code": "invalid_state"})
		return
	}
	h.deviceFlowsMu.Lock()
	flow := h.deviceFlows[state]
	h.deviceFlowsMu.Unlock()
	if flow == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Device authentication was not found. Start again.", "code": "not_found"})
		return
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.public.Status == "pending" {
		if !time.Now().Before(flow.public.ExpiresAt) {
			flow.public.Status, flow.public.Code, flow.public.Error = "expired", "expired", "The device code expired. Start again to get a new code."
			flow.cancel()
		} else if cancel {
			flow.public.Status = "cancelled"
			flow.cancel()
		}
	}
	c.JSON(http.StatusOK, flow.public)
}

// CancelDeviceAuthFlows blocks new starts and cancels pending acquisition without
// waiting for credential persistence. The worker publishes its terminal status;
// a commit already in progress must finish with its actual persistence result.
func (h *Handler) CancelDeviceAuthFlows() {
	h.deviceFlowsMu.Lock()
	h.deviceAuthClosed = true
	for _, cancel := range h.deviceAuthStarts {
		cancel()
	}
	flows := make([]*codexDeviceFlow, 0, len(h.deviceAuthWorkers))
	for flow := range h.deviceAuthWorkers {
		flows = append(flows, flow)
	}
	h.deviceFlowsMu.Unlock()
	for _, flow := range flows {
		// cancel is immutable and safe to call concurrently. Taking flow.mu here
		// would let an uncancellable storage commit defeat the shutdown context.
		flow.cancel()
	}
}

// WaitDeviceAuthFlows fences new acquisition and drains accepted workers within
// the shutdown grace period. Track workers independently of retained public
// statuses so even a slow commit whose status expired remains part of shutdown.
func (h *Handler) WaitDeviceAuthFlows(ctx context.Context) error {
	h.CancelDeviceAuthFlows()
	h.deviceFlowsMu.Lock()
	done := make([]<-chan struct{}, 0, len(h.deviceAuthWorkers))
	for flow := range h.deviceAuthWorkers {
		done = append(done, flow.done)
	}
	h.deviceFlowsMu.Unlock()
	for _, workerDone := range done {
		// Prefer an already completed worker even when the grace period expired.
		select {
		case <-workerDone:
			continue
		default:
		}
		select {
		case <-workerDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
