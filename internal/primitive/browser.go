package primitive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"primitivebox/internal/eventing"

	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
)

const defaultBrowserSessionTTL = 5 * time.Minute

type browserGotoParams struct {
	URL       string `json:"url"`
	SessionID string `json:"session_id,omitempty"`
	TimeoutS  int    `json:"timeout_s,omitempty"`
}

type browserExtractParams struct {
	SessionID string `json:"session_id"`
	Selector  string `json:"selector"`
}

type browserClickParams struct {
	SessionID string `json:"session_id"`
	Selector  string `json:"selector"`
	TimeoutS  int    `json:"timeout_s,omitempty"`
}

type browserScreenshotParams struct {
	SessionID string `json:"session_id"`
	FullPage  bool   `json:"full_page,omitempty"`
}

type browserGotoResult struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
}

type browserExtractResult struct {
	SessionID string `json:"session_id"`
	Selector  string `json:"selector"`
	Text      string `json:"text"`
}

type browserClickResult struct {
	SessionID string `json:"session_id"`
	Selector  string `json:"selector"`
	URL       string `json:"url"`
}

type browserScreenshotResult struct {
	SessionID   string `json:"session_id"`
	ContentType string `json:"content_type"`
	ImageBase64 string `json:"image_base64"`
}

type BrowserSessionManager struct {
	mu         sync.Mutex
	sessionTTL time.Duration
	sessions   map[string]*browserSession
	options    Options
}

type browserSession struct {
	id         string
	ctx        context.Context
	cancel     context.CancelFunc
	currentURL string
	lastUsedAt time.Time
	profileDir string
}

type browserGotoPrimitive struct {
	manager *BrowserSessionManager
	options Options
}

type browserExtractPrimitive struct {
	manager *BrowserSessionManager
	options Options
}

type browserClickPrimitive struct {
	manager *BrowserSessionManager
	options Options
}

type browserScreenshotPrimitive struct {
	manager *BrowserSessionManager
	options Options
}

func NewBrowserSessionManager(options Options) *BrowserSessionManager {
	return &BrowserSessionManager{
		sessionTTL: defaultBrowserSessionTTL,
		sessions:   make(map[string]*browserSession),
		options:    options,
	}
}

func NewBrowserGoto(workspaceDir string, manager *BrowserSessionManager, options Options) Primitive {
	_ = workspaceDir
	return &browserGotoPrimitive{manager: manager, options: options}
}

func NewBrowserExtract(workspaceDir string, manager *BrowserSessionManager, options Options) Primitive {
	_ = workspaceDir
	return &browserExtractPrimitive{manager: manager, options: options}
}

func NewBrowserClick(workspaceDir string, manager *BrowserSessionManager, options Options) Primitive {
	_ = workspaceDir
	return &browserClickPrimitive{manager: manager, options: options}
}

func NewBrowserScreenshot(workspaceDir string, manager *BrowserSessionManager, options Options) Primitive {
	_ = workspaceDir
	return &browserScreenshotPrimitive{manager: manager, options: options}
}

func (p *browserGotoPrimitive) Name() string     { return "browser.goto" }
func (p *browserGotoPrimitive) Category() string { return "browser" }

func (p *browserGotoPrimitive) Schema() Schema {
	return Schema{
		Name:        p.Name(),
		Description: "Navigate a sandbox-local browser session to a URL.",
		Input:       json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"},"session_id":{"type":"string"},"timeout_s":{"type":"integer"}},"required":["url"]}`),
		Output:      json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"url":{"type":"string"},"title":{"type":"string"}},"required":["session_id","url","title"]}`),
	}
}

func (p *browserGotoPrimitive) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var input browserGotoParams
	if err := json.Unmarshal(params, &input); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}
	if _, err := validateBrowserURL(input.URL); err != nil {
		return Result{}, err
	}
	session, err := p.manager.getOrCreate(input.SessionID)
	if err != nil {
		return Result{}, err
	}
	timeout := timeoutDuration(input.TimeoutS, 30)
	runCtx, cancel := context.WithTimeout(session.ctx, timeout)
	defer cancel()

	emitBrowserProgress(ctx, p.Name(), "launch", map[string]any{"session_id": session.id})
	var title string
	if err := chromedp.Run(runCtx,
		chromedp.Navigate(input.URL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Title(&title),
	); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	p.manager.touch(session.id, input.URL)
	result := browserGotoResult{SessionID: session.id, URL: input.URL, Title: title}
	emitBrowserProgress(ctx, p.Name(), "navigate_completed", map[string]any{"session_id": session.id, "url": input.URL})
	return Result{Data: result}, nil
}

func (p *browserExtractPrimitive) Name() string     { return "browser.extract" }
func (p *browserExtractPrimitive) Category() string { return "browser" }

func (p *browserExtractPrimitive) Schema() Schema {
	return Schema{
		Name:        p.Name(),
		Description: "Extract text from the current page for a CSS selector.",
		Input:       json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"selector":{"type":"string"}},"required":["session_id","selector"]}`),
		Output:      json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"selector":{"type":"string"},"text":{"type":"string"}},"required":["session_id","selector","text"]}`),
	}
}

func (p *browserExtractPrimitive) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var input browserExtractParams
	if err := json.Unmarshal(params, &input); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}
	session, err := p.manager.get(input.SessionID)
	if err != nil {
		return Result{}, err
	}
	var text string
	emitBrowserProgress(ctx, p.Name(), "extract_started", map[string]any{"session_id": session.id, "selector": input.Selector})
	if err := chromedp.Run(session.ctx, chromedp.Text(input.Selector, &text, chromedp.ByQuery)); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	p.manager.touch(session.id, session.currentURL)
	result := browserExtractResult{SessionID: session.id, Selector: input.Selector, Text: text}
	emitBrowserProgress(ctx, p.Name(), "extract_completed", map[string]any{"session_id": session.id, "selector": input.Selector})
	return Result{Data: result}, nil
}

func (p *browserClickPrimitive) Name() string     { return "browser.click" }
func (p *browserClickPrimitive) Category() string { return "browser" }

func (p *browserClickPrimitive) Schema() Schema {
	return Schema{
		Name:        p.Name(),
		Description: "Click a selector in an existing browser session.",
		Input:       json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"selector":{"type":"string"},"timeout_s":{"type":"integer"}},"required":["session_id","selector"]}`),
		Output:      json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"selector":{"type":"string"},"url":{"type":"string"}},"required":["session_id","selector","url"]}`),
	}
}

func (p *browserClickPrimitive) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var input browserClickParams
	if err := json.Unmarshal(params, &input); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}
	session, err := p.manager.get(input.SessionID)
	if err != nil {
		return Result{}, err
	}
	timeout := timeoutDuration(input.TimeoutS, 10)
	runCtx, cancel := context.WithTimeout(session.ctx, timeout)
	defer cancel()
	var currentURL string
	emitBrowserProgress(ctx, p.Name(), "click_started", map[string]any{"session_id": session.id, "selector": input.Selector})
	if err := chromedp.Run(runCtx,
		chromedp.WaitVisible(input.Selector, chromedp.ByQuery),
		chromedp.Click(input.Selector, chromedp.ByQuery),
		chromedp.Location(&currentURL),
	); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	p.manager.touch(session.id, currentURL)
	result := browserClickResult{SessionID: session.id, Selector: input.Selector, URL: currentURL}
	emitBrowserProgress(ctx, p.Name(), "click_completed", map[string]any{"session_id": session.id, "selector": input.Selector})
	return Result{Data: result}, nil
}

func (p *browserScreenshotPrimitive) Name() string     { return "browser.screenshot" }
func (p *browserScreenshotPrimitive) Category() string { return "browser" }

func (p *browserScreenshotPrimitive) Schema() Schema {
	return Schema{
		Name:        p.Name(),
		Description: "Capture a base64-encoded PNG screenshot of the current page.",
		Input:       json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"full_page":{"type":"boolean"}},"required":["session_id"]}`),
		Output:      json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"content_type":{"type":"string"},"image_base64":{"type":"string"}},"required":["session_id","content_type","image_base64"]}`),
	}
}

func (p *browserScreenshotPrimitive) Execute(ctx context.Context, params json.RawMessage) (Result, error) {
	var input browserScreenshotParams
	if err := json.Unmarshal(params, &input); err != nil {
		return Result{}, &PrimitiveError{Code: ErrValidation, Message: "invalid params: " + err.Error()}
	}
	session, err := p.manager.get(input.SessionID)
	if err != nil {
		return Result{}, err
	}
	emitBrowserProgress(ctx, p.Name(), "screenshot_started", map[string]any{"session_id": session.id, "full_page": input.FullPage})
	var screenshot []byte
	action := chromedp.CaptureScreenshot(&screenshot)
	if input.FullPage {
		action = chromedp.FullScreenshot(&screenshot, 90)
	}
	if err := chromedp.Run(session.ctx, action); err != nil {
		return Result{}, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	p.manager.touch(session.id, session.currentURL)
	result := browserScreenshotResult{
		SessionID:   session.id,
		ContentType: "image/png",
		ImageBase64: base64.StdEncoding.EncodeToString(screenshot),
	}
	emitBrowserProgress(ctx, p.Name(), "screenshot_completed", map[string]any{"session_id": session.id})
	return Result{Data: result}, nil
}

func (m *BrowserSessionManager) getOrCreate(sessionID string) (*browserSession, error) {
	m.cleanupExpired()
	if sessionID != "" {
		return m.get(sessionID)
	}
	return m.create()
}

func (m *BrowserSessionManager) get(sessionID string) (*browserSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, &PrimitiveError{Code: ErrValidation, Message: "session_id is required"}
	}
	m.cleanupExpired()
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, &PrimitiveError{Code: ErrNotFound, Message: "browser session not found"}
	}
	session.lastUsedAt = time.Now().UTC()
	return session, nil
}

func (m *BrowserSessionManager) touch(sessionID, currentURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session, ok := m.sessions[sessionID]; ok {
		session.currentURL = currentURL
		session.lastUsedAt = time.Now().UTC()
	}
}

func (m *BrowserSessionManager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for id, session := range m.sessions {
		if now.Sub(session.lastUsedAt) > m.sessionTTL {
			session.cancel()
			delete(m.sessions, id)
		}
	}
}

func (m *BrowserSessionManager) create() (*browserSession, error) {
	executable, err := findBrowserExecutable()
	if err != nil {
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	profileDir, err := os.MkdirTemp("", "primitivebox-browser-*")
	if err != nil {
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	configDir := profileDir + "/config"
	cacheDir := profileDir + "/cache"
	runtimeDir := profileDir + "/runtime"
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		_ = os.RemoveAll(profileDir)
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(executable),
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
			chromedp.Headless,
			chromedp.DisableGPU,
			chromedp.NoSandbox,
			chromedp.Flag("disable-crash-reporter", true),
			chromedp.Flag("disable-crashpad", true),
			chromedp.Flag("disable-breakpad", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("noerrdialogs", true),
			chromedp.Flag("disable-background-networking", true),
			chromedp.Flag("disable-default-apps", true),
			chromedp.Flag("password-store", "basic"),
			chromedp.Flag("user-data-dir", profileDir),
			chromedp.Env(
				"HOME="+profileDir,
				"XDG_CONFIG_HOME="+configDir,
				"XDG_CACHE_HOME="+cacheDir,
				"XDG_RUNTIME_DIR="+runtimeDir,
			),
		)...,
	)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	tabCtx, tabCancel := chromedp.NewContext(browserCtx)
	cancel := func() {
		tabCancel()
		browserCancel()
		allocCancel()
		_ = os.RemoveAll(profileDir)
	}
	session := &browserSession{
		id:         "browser-" + uuid.New().String()[:8],
		ctx:        tabCtx,
		cancel:     cancel,
		lastUsedAt: time.Now().UTC(),
		profileDir: profileDir,
	}
	if err := chromedp.Run(browserCtx); err != nil {
		cancel()
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	if err := chromedp.Run(tabCtx); err != nil {
		cancel()
		return nil, &PrimitiveError{Code: ErrExecution, Message: err.Error()}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.id] = session
	return session, nil
}

func emitBrowserProgress(ctx context.Context, method, message string, payload map[string]any) {
	eventing.Emit(ctx, eventing.Event{
		Type:    "browser.progress",
		Source:  "primitive",
		Method:  method,
		Message: message,
		Data:    eventing.MustJSON(payload),
	})
}

func validateBrowserURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, &PrimitiveError{Code: ErrValidation, Message: "invalid url"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, &PrimitiveError{Code: ErrValidation, Message: "url must use http or https"}
	}
	if parsed.Host == "" {
		return nil, &PrimitiveError{Code: ErrValidation, Message: "url host is required"}
	}
	return parsed, nil
}

func findBrowserExecutable() (string, error) {
	candidates := []string{
		"chromium",
		"chromium-browser",
		"google-chrome",
		"Google Chrome",
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("chromium executable not found in sandbox image")
}

func timeoutDuration(seconds, fallback int) time.Duration {
	if seconds <= 0 {
		seconds = fallback
	}
	return time.Duration(seconds) * time.Second
}
