package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type testCodexDeviceService struct {
	challenge   *codex.DeviceChallenge
	startErr    error
	completeErr error
	release     chan struct{}
	started     chan struct{}
	interval    time.Duration
}

func (s *testCodexDeviceService) Start(context.Context) (*codex.DeviceChallenge, error) {
	return s.challenge, s.startErr
}
func (s *testCodexDeviceService) Complete(ctx context.Context, _ *codex.DeviceChallenge, interval func(time.Duration)) (*codex.CodexAuthBundle, error) {
	if s.interval > 0 {
		interval(s.interval)
	}
	close(s.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
	}
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"email":"person@example.com","https://api.openai.com/auth":{"chatgpt_account_id":"account-1","chatgpt_plan_type":"plus"}}`)) + ".signature"
	return &codex.CodexAuthBundle{TokenData: codex.CodexTokenData{AccessToken: "private-access-token", RefreshToken: "private-refresh-token", IDToken: token, AccountID: "account-1", Email: "person@example.com"}}, nil
}
func setupDeviceHandler(t *testing.T) (*Handler, *testCodexDeviceService) {
	t.Helper()
	service := &testCodexDeviceService{challenge: &codex.DeviceChallenge{VerificationURI: codex.DeviceVerificationURI, UserCode: "ABCD-EFGH", Interval: 5 * time.Second, ExpiresAt: time.Now().Add(15 * time.Minute)}, release: make(chan struct{}), started: make(chan struct{})}
	h := &Handler{cfg: &config.Config{AuthDir: t.TempDir()}, authManager: coreauth.NewManager(nil, nil, nil), tokenStore: sdkauth.NewFileTokenStore()}
	h.deviceAuthFactory = func(*config.Config) codexDeviceService { return service }
	t.Cleanup(h.CancelDeviceAuthFlows)
	return h, service
}
func deviceRequest(h *Handler, method, query, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/v0/management/codex/device-auth"+query, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	switch method {
	case http.MethodPost:
		h.StartCodexDeviceAuth(c)
	case http.MethodDelete:
		h.CancelCodexDeviceAuth(c)
	default:
		h.GetCodexDeviceAuth(c)
	}
	return w
}
func startDeviceTest(t *testing.T, h *Handler, s *testCodexDeviceService) (codexDeviceStatus, *codexDeviceFlow) {
	t.Helper()
	w := deviceRequest(h, http.MethodPost, "", "{}")
	if w.Code != 200 {
		t.Fatalf("start %d: %s", w.Code, w.Body)
	}
	var response codexDeviceStatus
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.State) != 43 || response.Status != "pending" || response.UserCode != "ABCD-EFGH" || response.Interval != 5 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid challenge: %+v", response)
	}
	<-s.started
	h.deviceFlowsMu.Lock()
	flow := h.deviceFlows[response.State]
	h.deviceFlowsMu.Unlock()
	return response, flow
}
func readDeviceTest(t *testing.T, h *Handler, state string) codexDeviceStatus {
	t.Helper()
	w := deviceRequest(h, http.MethodGet, "?state="+state, "")
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "device_auth_id") || strings.Contains(w.Body.String(), "code_verifier") {
		t.Fatalf("private data exposed: %s", w.Body)
	}
	var response codexDeviceStatus
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	return response
}

func TestCodexDeviceAuthPersistsAndRegistersBeforeComplete(t *testing.T) {
	h, s := setupDeviceHandler(t)
	s.interval = 10 * time.Second
	response, flow := startDeviceTest(t, h, s)
	if got := readDeviceTest(t, h, response.State); got.Interval != 10 || got.Status != "pending" {
		t.Fatalf("slowdown not visible: %+v", got)
	}
	close(s.release)
	<-flow.done
	got := readDeviceTest(t, h, response.State)
	if got.Status != "complete" || got.Account == nil || got.Account.Email != "person@example.com" || got.Account.PlanType != "plus" {
		t.Fatalf("not completed: %+v", got)
	}
	records := h.authManager.List()
	if len(records) != 1 || records[0].Metadata["access_token"] != "private-access-token" || records[0].Metadata["refresh_token"] != "private-refresh-token" {
		t.Fatalf("runtime credentials missing: %+v", records)
	}
	if _, err := os.Stat(filepath.Join(h.cfg.AuthDir, got.Account.Name)); err != nil {
		t.Fatal(err)
	}
	w := deviceRequest(h, http.MethodDelete, "?state="+response.State, "")
	if !strings.Contains(w.Body.String(), `"status":"complete"`) {
		t.Fatalf("late cancellation changed completed flow: %s", w.Body)
	}
}

func TestCodexDeviceAuthCancelBeforeSave(t *testing.T) {
	h, s := setupDeviceHandler(t)
	response, flow := startDeviceTest(t, h, s)
	w := deviceRequest(h, http.MethodDelete, "?state="+response.State, "")
	if !strings.Contains(w.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("not cancelled: %s", w.Body)
	}
	close(s.release)
	<-flow.done
	if got := readDeviceTest(t, h, response.State); got.Status != "cancelled" {
		t.Fatal(got.Status)
	}
	entries, err := os.ReadDir(h.cfg.AuthDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || len(h.authManager.List()) != 0 {
		t.Fatal("cancelled flow installed credentials")
	}
}

func TestCodexDeviceAuthCancellationDuringSaveObservesCommit(t *testing.T) {
	h, s := setupDeviceHandler(t)
	saveStarted, saveRelease := make(chan struct{}), make(chan struct{})
	h.postAuthHook = func(context.Context, *coreauth.Auth) error { close(saveStarted); <-saveRelease; return nil }
	response, flow := startDeviceTest(t, h, s)
	close(s.release)
	<-saveStarted
	cancelled := make(chan *httptest.ResponseRecorder, 1)
	go func() { cancelled <- deviceRequest(h, http.MethodDelete, "?state="+response.State, "") }()
	close(saveRelease)
	<-flow.done
	w := <-cancelled
	if !strings.Contains(w.Body.String(), `"status":"complete"`) || len(h.authManager.List()) != 1 {
		t.Fatalf("cancellation lied after persistence started: %s", w.Body)
	}
}

func TestCodexDeviceAuthShutdownDoesNotWaitForCommit(t *testing.T) {
	h, s := setupDeviceHandler(t)
	saveStarted, saveRelease := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(saveRelease) }) }
	t.Cleanup(release)
	h.postAuthHook = func(ctx context.Context, _ *coreauth.Auth) error {
		if ctx.Done() != nil {
			return errors.New("persistence must outlive acquisition cancellation")
		}
		close(saveStarted)
		<-saveRelease
		return nil
	}
	response, flow := startDeviceTest(t, h, s)
	close(s.release)
	<-saveStarted

	// Another acquisition must be canceled even while the first flow owns its
	// mutex across persistence. Its worker should be the one to publish status.
	pendingService := &testCodexDeviceService{challenge: s.challenge, release: make(chan struct{}), started: make(chan struct{})}
	h.deviceAuthFactory = func(*config.Config) codexDeviceService { return pendingService }
	pendingResponse, pendingFlow := startDeviceTest(t, h, pendingService)
	shutdownReturned := make(chan struct{})
	go func() {
		h.CancelDeviceAuthFlows()
		h.CancelDeviceAuthFlows() // Repeated shutdown does not enqueue more work.
		close(shutdownReturned)
	}()
	select {
	case <-shutdownReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited for credential persistence")
	}
	select {
	case <-pendingFlow.done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel the other acquisition")
	}
	if got := readDeviceTest(t, h, pendingResponse.State); got.Status != "cancelled" {
		t.Fatalf("pending flow was not cancelled: %+v", got)
	}
	if w := deviceRequest(h, http.MethodPost, "", "{}"); w.Code != http.StatusServiceUnavailable {
		t.Fatal("accepted a new flow after shutdown")
	}
	select {
	case <-flow.done:
		t.Fatal("commit finished before persistence was released")
	default:
	}
	release()
	<-flow.done
	if got := readDeviceTest(t, h, response.State); got.Status != "complete" || len(h.authManager.List()) != 1 {
		t.Fatalf("shutdown changed the committed result: %+v", got)
	}
	w := deviceRequest(h, http.MethodDelete, "?state="+response.State, "")
	if !strings.Contains(w.Body.String(), `"status":"complete"`) {
		t.Fatalf("DELETE misreported shutdown commit: %s", w.Body)
	}
}

func TestCodexDeviceAuthExpiryFailureAndSafeErrors(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		h, s := setupDeviceHandler(t)
		response, flow := startDeviceTest(t, h, s)
		flow.mu.Lock()
		flow.public.ExpiresAt = time.Now().Add(-time.Second)
		flow.mu.Unlock()
		if got := readDeviceTest(t, h, response.State); got.Status != "expired" {
			t.Fatal(got.Status)
		}
		<-flow.done
		if len(h.authManager.List()) != 0 {
			t.Fatal("expired flow registered")
		}
	})
	for _, failure := range []string{"start", "complete", "save"} {
		t.Run(failure, func(t *testing.T) {
			h, s := setupDeviceHandler(t)
			switch failure {
			case "start":
				s.startErr = errors.New("private-provider-secret")
			case "complete":
				s.completeErr = errors.New("private-provider-secret")
			case "save":
				h.postAuthHook = func(context.Context, *coreauth.Auth) error { return errors.New("private-provider-secret") }
			}
			if failure == "start" {
				w := deviceRequest(h, http.MethodPost, "", "{}")
				if w.Code != 502 || strings.Contains(w.Body.String(), "private") {
					t.Fatalf("unsafe start error %s", w.Body)
				}
				return
			}
			response, flow := startDeviceTest(t, h, s)
			close(s.release)
			<-flow.done
			if got := readDeviceTest(t, h, response.State); got.Status != "error" || got.Code == "" {
				t.Fatalf("bad failure status %+v", got)
			}
			if len(h.authManager.List()) != 0 {
				t.Fatal("failed flow registered")
			}
		})
	}
}

func TestCodexDeviceAuthReloadAndShutdown(t *testing.T) {
	h, s := setupDeviceHandler(t)
	originalDir := h.cfg.AuthDir
	var capturedProxy string
	h.cfg.ProxyURL = "http://original-proxy.example"
	h.deviceAuthFactory = func(cfg *config.Config) codexDeviceService { capturedProxy = cfg.ProxyURL; return s }
	response, flow := startDeviceTest(t, h, s)
	newDir := t.TempDir()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			h.SetConfig(&config.Config{AuthDir: newDir, SDKConfig: config.SDKConfig{ProxyURL: "http://new-proxy.example"}})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			readDeviceTest(t, h, response.State)
		}
	}()
	wg.Wait()
	close(s.release)
	<-flow.done
	if capturedProxy != "http://original-proxy.example" {
		t.Fatal("start did not use proxy snapshot")
	}
	got := readDeviceTest(t, h, response.State)
	if got.Status != "complete" {
		t.Fatalf("reload broke authentication: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(newDir, got.Account.Name)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(originalDir)
	if len(entries) != 0 {
		t.Fatal("persisted to obsolete auth directory")
	}
	h2, s2 := setupDeviceHandler(t)
	r2, f2 := startDeviceTest(t, h2, s2)
	h2.CancelDeviceAuthFlows()
	<-f2.done
	if got := readDeviceTest(t, h2, r2.State); got.Status != "cancelled" {
		t.Fatal(got.Status)
	}
}

func TestCodexDeviceAuthValidatesRequests(t *testing.T) {
	h, _ := setupDeviceHandler(t)
	for _, body := range []string{"", `null`, `{"url":"https://attacker.example"}`, `[]`} {
		if w := deviceRequest(h, http.MethodPost, "", body); w.Code != 400 {
			t.Fatalf("accepted %s: %d", body, w.Code)
		}
	}
	if w := deviceRequest(h, http.MethodGet, "", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := deviceRequest(h, http.MethodGet, "?state=unknown", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}

type blockingDeviceStart struct {
	entered chan struct{}
}

func (s *blockingDeviceStart) Start(ctx context.Context) (*codex.DeviceChallenge, error) {
	close(s.entered)
	<-ctx.Done()
	// Even a provider that races with cancellation must not publish a late flow.
	return &codex.DeviceChallenge{VerificationURI: codex.DeviceVerificationURI, UserCode: "late", Interval: 5 * time.Second, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (s *blockingDeviceStart) Complete(context.Context, *codex.DeviceChallenge, func(time.Duration)) (*codex.CodexAuthBundle, error) {
	panic("shutdown flow must never poll")
}
func TestCodexDeviceAuthShutdownDuringStart(t *testing.T) {
	h, _ := setupDeviceHandler(t)
	service := &blockingDeviceStart{entered: make(chan struct{})}
	h.deviceAuthFactory = func(*config.Config) codexDeviceService { return service }
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- deviceRequest(h, http.MethodPost, "", "{}") }()
	<-service.entered
	h.CancelDeviceAuthFlows()
	if w := <-response; w.Code != http.StatusServiceUnavailable {
		t.Fatalf("late start was published: %d %s", w.Code, w.Body)
	}
	if w := deviceRequest(h, http.MethodPost, "", "{}"); w.Code != http.StatusServiceUnavailable {
		t.Fatal("accepted start after shutdown")
	}
	h.deviceFlowsMu.Lock()
	defer h.deviceFlowsMu.Unlock()
	if len(h.deviceFlows) != 0 || len(h.deviceAuthStarts) != 0 {
		t.Fatal("shutdown retained a pending flow")
	}
}

// Observe when the drain begins waiting, without depending on scheduling delays.
type observedDeviceDrainContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedDeviceDrainContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func blockedDeviceCommit(t *testing.T) (*Handler, codexDeviceStatus, *codexDeviceFlow, func()) {
	t.Helper()
	h, service := setupDeviceHandler(t)
	entered, unblocked := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblocked) }) }
	h.postAuthHook = func(ctx context.Context, _ *coreauth.Auth) error {
		if ctx.Done() != nil {
			return errors.New("credential persistence must be detached from acquisition cancellation")
		}
		close(entered)
		<-unblocked
		return nil
	}
	response, flow := startDeviceTest(t, h, service)
	t.Cleanup(func() {
		release()
		<-flow.done
	})
	close(service.release)
	<-entered
	return h, response, flow, release
}

func TestCodexDeviceAuthDrainWaitsForPersistenceAndRegistration(t *testing.T) {
	for _, retireStatus := range []bool{false, true} {
		name := "retained status"
		if retireStatus {
			name = "public status already retired"
		}
		t.Run(name, func(t *testing.T) {
			h, response, flow, release := blockedDeviceCommit(t)
			if retireStatus {
				// Simulate the retention callback removing the public result while a
				// slow storage commit is still running; do not wait for wall time.
				h.deviceFlowsMu.Lock()
				delete(h.deviceFlows, response.State)
				_, tracked := h.deviceAuthWorkers[flow]
				h.deviceFlowsMu.Unlock()
				if !tracked {
					t.Fatal("active commit disappeared with its public status")
				}
				if w := deviceRequest(h, http.MethodGet, "?state="+response.State, ""); w.Code != http.StatusNotFound {
					t.Fatalf("retired status remained public: %d", w.Code)
				}
			}
			ctx := &observedDeviceDrainContext{Context: context.Background(), waiting: make(chan struct{})}
			drained := make(chan error, 1)
			go func() { drained <- h.WaitDeviceAuthFlows(ctx) }()
			select {
			case <-ctx.waiting:
			case errDrain := <-drained:
				t.Fatalf("drain returned before unfinished persistence: %v", errDrain)
			case <-time.After(5 * time.Second):
				t.Fatal("drain did not reach its context-aware wait")
			}
			select {
			case errDrain := <-drained:
				t.Fatalf("drain returned before persistence was released: %v", errDrain)
			default:
			}
			if len(h.authManager.List()) != 0 {
				t.Fatal("account registered before persistence completed")
			}
			if w := deviceRequest(h, http.MethodPost, "", "{}"); w.Code != http.StatusServiceUnavailable {
				t.Fatal("draining gateway accepted a new device flow")
			}
			release()
			if errDrain := <-drained; errDrain != nil {
				t.Fatalf("drain failed after commit completed: %v", errDrain)
			}
			select {
			case <-flow.done:
			default:
				t.Fatal("drain returned before the worker completed")
			}
			flow.mu.Lock()
			result := flow.public
			flow.mu.Unlock()
			if result.Status != "complete" || result.Account == nil {
				t.Fatalf("shutdown lost the committed account: %+v", result)
			}
			data, errRead := os.ReadFile(filepath.Join(h.cfg.AuthDir, result.Account.Name))
			if errRead != nil {
				t.Fatal(errRead)
			}
			var saved codex.CodexTokenStorage
			if errDecode := json.Unmarshal(data, &saved); errDecode != nil {
				t.Fatalf("drain left an incomplete credential file: %v", errDecode)
			}
			if saved.AccessToken != "private-access-token" || saved.RefreshToken != "private-refresh-token" {
				t.Fatal("drain returned without saved credential tokens")
			}
			if len(h.authManager.List()) != 1 {
				t.Fatal("drain returned before runtime registration completed")
			}
		})
	}
}

func TestCodexDeviceAuthDrainHonorsDeadlineAndCanBeRetried(t *testing.T) {
	for _, scenario := range []string{"cancelled", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			h, _, flow, release := blockedDeviceCommit(t)
			var ctx context.Context
			var cancel context.CancelFunc
			want := context.Canceled
			if scenario == "expired" {
				ctx, cancel = context.WithDeadline(context.Background(), time.Unix(1, 0))
				want = context.DeadlineExceeded
			} else {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			defer cancel()
			if errDrain := h.WaitDeviceAuthFlows(ctx); !errors.Is(errDrain, want) {
				t.Fatalf("unfinished drain returned %v, want %v", errDrain, want)
			}
			select {
			case <-flow.done:
				t.Fatal("expired grace period unexpectedly interrupted persistence")
			default:
			}
			h.deviceFlowsMu.Lock()
			_, tracked := h.deviceAuthWorkers[flow]
			h.deviceFlowsMu.Unlock()
			if !tracked {
				t.Fatal("failed drain forgot an unfinished worker")
			}
			release()
			if errDrain := h.WaitDeviceAuthFlows(context.Background()); errDrain != nil {
				t.Fatalf("a later drain failed: %v", errDrain)
			}
			if errDrain := h.WaitDeviceAuthFlows(ctx); errDrain != nil {
				t.Fatalf("completed workers should not fail an already expired drain: %v", errDrain)
			}
			if len(h.authManager.List()) != 1 {
				t.Fatal("deadline path discarded the eventual account registration")
			}
		})
	}
}

func TestCodexDeviceAuthDrainCancelsAcquisitionAfterPublicStatusRetirement(t *testing.T) {
	h, service := setupDeviceHandler(t)
	response, flow := startDeviceTest(t, h, service)
	h.deviceFlowsMu.Lock()
	delete(h.deviceFlows, response.State)
	h.deviceFlowsMu.Unlock()
	if errDrain := h.WaitDeviceAuthFlows(context.Background()); errDrain != nil {
		t.Fatal(errDrain)
	}
	select {
	case <-flow.done:
	default:
		t.Fatal("drain ignored the acquisition whose public status was retired")
	}
	flow.mu.Lock()
	status := flow.public.Status
	flow.mu.Unlock()
	if status != "cancelled" || len(h.authManager.List()) != 0 {
		t.Fatalf("retired acquisition was not cancelled cleanly: %s", status)
	}
}
