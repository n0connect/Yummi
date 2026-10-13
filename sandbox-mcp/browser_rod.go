package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
	"github.com/ysmood/gson"
)

// RodDriver implements BrowserDriver using go-rod and stealth.
type RodDriver struct {
	mu              sync.Mutex
	browser         *rod.Browser
	launcherURL     string
	activePage      *rod.Page
	currentRefs     map[string]*rod.Element
	currentElems    []SnapshotElement
	logsBuffer      map[string][]LogEntry
	logGeneration   uint64
	contexts        map[string]*rod.Browser
	activeSession   string
	headless        bool
	requestRouter   *rod.HijackRouter
	connectionProxy *browserProxy
}

// NewRodDriver creates a new RodDriver instance.
func NewRodDriver() *RodDriver {
	return &RodDriver{
		currentRefs: make(map[string]*rod.Element),
		logsBuffer:  make(map[string][]LogEntry),
		contexts:    make(map[string]*rod.Browser),
	}
}

// ensureBrowser ensures a running browser instance is connected.
func (d *RodDriver) ensureBrowser(ctx context.Context, reqHeadless *bool) error {
	// Must precede browser discovery, profile writes, lock removal and CDP calls.
	if err := requireSandboxRuntime(); err != nil {
		return err
	}
	if d.browser != nil {
		if reqHeadless != nil && *reqHeadless != d.headless {
			return fmt.Errorf("browser mode is already active; call browser_session reset before changing headless mode")
		}
		// Ping browser to ensure connection is still alive (bounded 1s timeout)
		if _, err := d.browser.Context(ctx).Timeout(1 * time.Second).Version(); err == nil {
			return nil
		}
		_ = d.browser.Close()
		d.browser = nil
		if d.connectionProxy != nil {
			_ = d.connectionProxy.Close()
			d.connectionProxy = nil
		}
	}

	display := os.Getenv("DISPLAY")
	if display == "" {
		display = ":1" // Default TigerVNC display in sandbox
	}

	// Find Chromium binary in container
	binPath := "/usr/bin/chromium"
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("sandbox Chromium missing: %w", err)
	}

	userDataDir := os.Getenv("CHROMIUM_USER_DATA_DIR")
	if userDataDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			userDataDir = filepath.Join(home, ".config", "chromium-agent", fmt.Sprintf("process-%d", os.Getpid()))
		} else {
			userDataDir = "/tmp/chromium-agent"
		}
	}
	if !filepath.IsAbs(userDataDir) || !hasPathPrefix(filepath.Clean(userDataDir), "/home/pentester") {
		return fmt.Errorf("browser profile must remain under sandbox home")
	}
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return fmt.Errorf("sandbox browser profile unavailable: %w", err)
	}
	cleanProfile, reason := confineDir(userDataDir)
	if reason != "" {
		return fmt.Errorf("sandbox browser profile: %s", reason)
	}
	userDataDir = cleanProfile
	// Chrome's documented Local State preferences keep DNS in the sandbox
	// and use the same Cloudflare resolver over HTTPS where port 53 is blocked.
	state := map[string]any{}
	statePath := filepath.Join(userDataDir, "Local State")
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &state)
	}
	if state == nil {
		state = make(map[string]any)
	}
	state["dns_over_https"] = map[string]any{"mode": "secure", "templates": "https://1.1.1.1/dns-query{?dns}"}
	state["async_dns"] = map[string]any{"enabled": true}
	stateBytes, _ := json.Marshal(state)
	if _, reason := writeConfinedFile(statePath, stateBytes); reason != "" {
		return fmt.Errorf("sandbox browser DNS profile: %s", reason)
	}
	_ = os.Remove(filepath.Join(userDataDir, "SingletonLock"))
	_ = os.Remove(filepath.Join(userDataDir, "SingletonSocket"))
	_ = os.Remove(filepath.Join(userDataDir, "SingletonCookie"))

	forceHeadless := os.Getenv("YUMMI_BROWSER_HEADLESS") == "true" || os.Getenv("YUMMI_BROWSER_HEADLESS") == "1"
	if reqHeadless != nil {
		forceHeadless = *reqHeadless
	}

	proxy, err := newBrowserProxy()
	if err != nil {
		return fmt.Errorf("sandbox browser connection guard: %w", err)
	}
	connected := false
	defer func() {
		if !connected {
			_ = proxy.Close()
		}
	}()
	l := launcher.New().
		Bin(binPath).
		Leakless(false).
		RemoteDebuggingPort(0).
		Set("proxy-server", "http://"+proxy.address).
		Set("proxy-bypass-list", "<-loopback>").
		Set("disable-quic").
		Set("force-webrtc-ip-handling-policy", "disable_non_proxied_udp").
		Set("display", display).
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-gpu").
		Set("window-size", "1600,900").
		Set("user-data-dir", userDataDir).
		Headless(forceHeadless)

	wsURL, err := l.Launch()
	if err != nil && !forceHeadless {
		// Fallback to headless if X11 display is unavailable
		l = launcher.New().
			Bin(binPath).
			Leakless(false).
			RemoteDebuggingPort(0).
			Set("proxy-server", "http://"+proxy.address).
			Set("proxy-bypass-list", "<-loopback>").
			Set("disable-quic").
			Set("force-webrtc-ip-handling-policy", "disable_non_proxied_udp").
			Set("no-sandbox").
			Set("disable-dev-shm-usage").
			Set("user-data-dir", userDataDir).
			Headless(true)
		wsURL, err = l.Launch()
		forceHeadless = true
		if err != nil {
			return fmt.Errorf("failed to launch chromium: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to launch chromium: %w", err)
	}

	d.launcherURL = wsURL
	browser := rod.New().ControlURL(wsURL)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("failed to connect to chromium: %w", err)
	}

	d.browser = browser
	d.connectionProxy = proxy
	d.headless = forceHeadless
	d.contexts = make(map[string]*rod.Browser)
	d.activeSession = "default"
	// All external browser requests, including redirects, WebSockets and new tabs,
	// consult the gateway's name policy before proceeding. Packet policy is still
	// enforced by the gateway itself. No policy mutation is available here.
	router := browser.HijackRequests()
	if err := router.Add("*", "", func(h *rod.Hijack) {
		u := h.Request.URL()
		scheme := strings.ToLower(u.Scheme)
		if scheme == "http" || scheme == "https" || scheme == "ws" || scheme == "wss" || h.Request.Type() == proto.NetworkResourceTypeWebSocket {
			host := u.Hostname()
			ip := net.ParseIP(host)
			if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				policyCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				policyErr := researchHostnameAllowed(policyCtx, host)
				cancel()
				if policyErr != nil {
					h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
					return
				}
			}
		}
		h.ContinueRequest(&proto.FetchContinueRequest{})
	}); err != nil {
		_ = browser.Close()
		d.browser = nil
		return fmt.Errorf("browser request policy setup failed: %w", err)
	}
	d.requestRouter = router
	go router.Run()
	connected = true
	return nil
}

func (d *RodDriver) activeBrowserLocked() (*rod.Browser, error) {
	label := d.activeSession
	if label == "" {
		label = "default"
		d.activeSession = label
	}
	if b := d.contexts[label]; b != nil {
		return b, nil
	}
	if len(d.contexts) >= 16 {
		return nil, fmt.Errorf("browser session limit reached; reset unused sessions")
	}
	b, err := d.browser.Incognito()
	if err != nil {
		return nil, fmt.Errorf("isolated browser session failed: %w", err)
	}
	d.contexts[label] = b
	return b, nil
}

// Rod.Pages lists targets across contexts; filter explicitly before exposing
// tabs or selecting a fallback page from the active isolated session.
func sessionPages(ctx context.Context, b *rod.Browser) (rod.Pages, error) {
	pages, err := b.Pages()
	if err != nil {
		return nil, err
	}
	var own rod.Pages
	for _, page := range pages {
		info, err := page.Context(ctx).Info()
		if err == nil && info.BrowserContextID == b.BrowserContextID {
			own = append(own, page)
		}
	}
	return own, nil
}

func (d *RodDriver) appendLogLocked(session string, generation uint64, entry LogEntry) {
	if generation != d.logGeneration {
		return
	}
	if session == "" {
		session = "default"
	}
	if d.logsBuffer == nil {
		d.logsBuffer = make(map[string][]LogEntry)
	}
	buf := d.logsBuffer[session]
	buf = append(buf, entry)
	if len(buf) > 500 {
		buf = buf[100:]
	}
	d.logsBuffer[session] = buf
}

// hookPageEvents attaches listeners for console logs, network errors, and auto-dismisses JavaScript dialogs.
func (d *RodDriver) hookPageEvents(page *rod.Page, sessionLabel string) {
	generation := d.logGeneration
	if page == nil {
		return
	}
	if sessionLabel == "" {
		sessionLabel = d.activeSession
	}
	if sessionLabel == "" {
		sessionLabel = "default"
	}
	// Enable Page and Network domains so Chromium dispatches dialog and network events
	_ = proto.PageEnable{}.Call(page)
	_ = proto.NetworkEnable{}.Call(page)

	// Additional CDP URL filtering. The sandbox connection proxy, not these
	// version-dependent URL filters or notification events, guards WS/WSS.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if patterns, err := getGatewayBlockedURLPatterns(ctx); err == nil && len(patterns) > 0 {
		_ = proto.NetworkSetBlockedURLs{Urls: patterns}.Call(page)
	}
	cancel()

	// Intercept and neutralize modal dialogs (alert, confirm, prompt) before page scripts run
	_, _ = page.EvalOnNewDocument(`
		window.alert = function(msg) { console.warn("[dialog.alert] " + msg); };
		window.confirm = function(msg) { console.warn("[dialog.confirm] " + msg); return true; };
		window.prompt = function(msg, def) { console.warn("[dialog.prompt] " + msg); return def || ""; };
	`)

	// Auto-dismiss/accept any JavaScript dialogs (alert, confirm, prompt) so they never freeze CDP
	go page.EachEvent(func(e *proto.PageJavascriptDialogOpening) {
		d.mu.Lock()
		d.appendLogLocked(sessionLabel, generation, LogEntry{
			Type:      "dialog." + string(e.Type),
			Text:      e.Message,
			Timestamp: time.Now().Format(time.RFC3339),
		})
		d.mu.Unlock()
		_ = proto.PageHandleJavaScriptDialog{
			Accept:     true,
			PromptText: "",
		}.Call(page)
	})()

	// Hook console logs
	go page.EachEvent(func(e *proto.RuntimeConsoleAPICalled) {
		d.mu.Lock()
		defer d.mu.Unlock()
		var msg []string
		for _, arg := range e.Args {
			msg = append(msg, fmt.Sprintf("%v", arg.Value))
		}
		d.appendLogLocked(sessionLabel, generation, LogEntry{
			Type:      "console." + string(e.Type),
			Text:      strings.Join(msg, " "),
			Timestamp: time.Now().Format(time.RFC3339),
		})
	})()

	// Hook outgoing network requests to capture API endpoints and HTTP methods
	go page.EachEvent(func(e *proto.NetworkRequestWillBeSent) {
		if e.Request == nil {
			return
		}
		u := e.Request.URL
		if strings.HasPrefix(u, "data:") || strings.HasPrefix(u, "blob:") {
			return
		}
		method := e.Request.Method
		d.mu.Lock()
		if len(u) > 120 {
			u = u[:117] + "..."
		}
		d.appendLogLocked(sessionLabel, generation, LogEntry{
			Type:      "network.request",
			Text:      fmt.Sprintf("[%s] %s", method, u),
			Timestamp: time.Now().Format(time.RFC3339),
		})
		d.mu.Unlock()
	})()

	// Diagnostic WebSocket events only. A notification cannot cancel a socket;
	// the connection proxy checks policy before any upstream connection opens.
	go page.EachEvent(func(e *proto.NetworkWebSocketCreated) {
		u := e.URL
		host := ""
		if parsed, err := url.Parse(u); err == nil {
			host = parsed.Hostname()
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) && host != "" {
			policyCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			policyErr := researchHostnameAllowed(policyCtx, host)
			cancel()
			if policyErr != nil {
				d.mu.Lock()
				d.appendLogLocked(sessionLabel, generation, LogEntry{
					Type:      "network.blocked",
					Text:      fmt.Sprintf("[WS_BLOCKED] %s (%v)", u, policyErr),
					Timestamp: time.Now().Format(time.RFC3339),
				})
				d.mu.Unlock()
				return
			}
		}
		d.mu.Lock()
		d.appendLogLocked(sessionLabel, generation, LogEntry{
			Type:      "network.websocket",
			Text:      fmt.Sprintf("[WS] %s", u),
			Timestamp: time.Now().Format(time.RFC3339),
		})
		d.mu.Unlock()
	})()

	// Hook network responses (capturing HTTP errors >= 400 and JSON API calls for pentest reconnaissance)
	go page.EachEvent(func(e *proto.NetworkResponseReceived) {
		if e.Response == nil {
			return
		}
		status := e.Response.Status
		isJSON := strings.Contains(strings.ToLower(e.Response.MIMEType), "json")
		if status >= 400 || isJSON {
			d.mu.Lock()
			entryType := "network.response"
			if status >= 400 {
				entryType = "network.error"
			}
			u := e.Response.URL
			if len(u) > 120 {
				u = u[:117] + "..."
			}
			d.appendLogLocked(sessionLabel, generation, LogEntry{
				Type:      entryType,
				Text:      fmt.Sprintf("[%d] %s (type=%s)", status, u, e.Response.MIMEType),
				Timestamp: time.Now().Format(time.RFC3339),
			})
			d.mu.Unlock()
		}
	})()
}

// getActivePageLocked returns the current active page, or attaches to an existing browser page,
// or creates a new page if none exist. Must be called while holding d.mu.
func (d *RodDriver) getActivePageLocked(ctx context.Context) (*rod.Page, error) {
	if d.activePage != nil {
		if _, err := d.activePage.Context(ctx).Timeout(1 * time.Second).Info(); err == nil {
			return d.activePage, nil
		}
		d.activePage = nil
	}
	if err := d.ensureBrowser(ctx, nil); err != nil {
		return nil, err
	}
	activeBrowser, err := d.activeBrowserLocked()
	if err != nil {
		return nil, err
	}
	pages, err := sessionPages(ctx, activeBrowser)
	if err == nil && len(pages) > 0 {
		// Prefer the most recent non-blank page
		for i := len(pages) - 1; i >= 0; i-- {
			if info, err := pages[i].Context(ctx).Timeout(1 * time.Second).Info(); err == nil && info != nil && info.URL != "" && info.URL != "about:blank" {
				d.activePage = pages[i]
				d.hookPageEvents(d.activePage, d.activeSession)
				return d.activePage, nil
			}
		}
		// Fallback to the last page
		d.activePage = pages[len(pages)-1]
		d.hookPageEvents(d.activePage, d.activeSession)
		return d.activePage, nil
	}
	page, err := stealth.Page(activeBrowser)
	if err != nil {
		page, err = activeBrowser.Page(proto.TargetCreateTarget{URL: ""})
		if err != nil {
			return nil, fmt.Errorf("failed to create page: %w", err)
		}
	}
	d.activePage = page
	d.hookPageEvents(d.activePage, d.activeSession)
	return d.activePage, nil
}

// Open navigates to a URL and returns page info with an initial snapshot.
func (d *RodDriver) Open(ctx context.Context, p OpenParams) (*PageInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.ensureBrowser(ctx, p.Headless); err != nil {
		return nil, err
	}
	label := strings.TrimSpace(p.Session)
	if label == "" {
		label = "default"
	}
	if !sessIDPattern.MatchString(label) {
		return nil, fmt.Errorf("session label must be 1-40 letters, digits, underscores or hyphens")
	}
	d.activeSession = label
	activeBrowser, err := d.activeBrowserLocked()
	if err != nil {
		return nil, err
	}

	page, err := stealth.Page(activeBrowser)
	if err != nil {
		page, err = activeBrowser.Page(proto.TargetCreateTarget{URL: ""})
		if err != nil {
			return nil, fmt.Errorf("failed to create page: %w", err)
		}
	}

	// Close any initial blank pages left behind by browser startup
	if allPages, err := sessionPages(ctx, activeBrowser); err == nil {
		for _, pge := range allPages {
			if pge != page {
				if info, _ := pge.Info(); info != nil && (info.URL == "" || info.URL == "about:blank") {
					_ = pge.Close()
				}
			}
		}
	}

	d.activePage = page
	d.currentRefs = make(map[string]*rod.Element)
	d.currentElems = nil

	// Ensure automatic download directory is set up
	downloadDir := "/home/pentester/work/downloads"
	_ = os.MkdirAll(downloadDir, 0o777)
	_ = proto.BrowserSetDownloadBehavior{
		Behavior:     proto.BrowserSetDownloadBehaviorBehaviorAllow,
		DownloadPath: downloadDir,
	}.Call(d.browser)

	d.hookPageEvents(page, label)

	// Inject custom HTTP headers if provided
	if len(p.Headers) > 0 {
		hdrMap := proto.NetworkHeaders{}
		for k, v := range p.Headers {
			hdrMap[k] = gson.New(v)
		}
		_ = proto.NetworkSetExtraHTTPHeaders{Headers: hdrMap}.Call(page)
	}

	var httpStatus atomic.Int64
	if strings.HasPrefix(p.URL, "http://") || strings.HasPrefix(p.URL, "https://") {
		statusCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go page.Context(statusCtx).EachEvent(func(e *proto.NetworkResponseReceived) bool {
			if e.Type == proto.NetworkResourceTypeDocument && e.Response != nil {
				httpStatus.Store(int64(e.Response.Status))
				return true
			}
			return false
		})()
	}
	if err := page.Context(ctx).Navigate(p.URL); err != nil {
		return nil, fmt.Errorf("navigate to %s: %w", p.URL, err)
	}

	switch p.WaitUntil {
	case "networkidle":
		_ = page.Context(ctx).WaitIdle(2 * time.Second)
	case "domcontentloaded":
		_ = page.Context(ctx).WaitDOMStable(1*time.Second, 0.1)
	default:
		_ = page.Context(ctx).WaitLoad()
	}

	info, _ := page.Info()
	title := ""
	currentURL := p.URL
	if info != nil {
		title = info.Title
		currentURL = info.URL
	}

	snap, _ := d.generateSnapshotLocked(ctx, true, true, 4, "")

	return &PageInfo{
		URL:             currentURL,
		Title:           title,
		HTTPStatus:      int(httpStatus.Load()),
		InitialSnapshot: snap,
	}, nil
}

// generateSnapshotLocked extracts accessibility/interactive element tree.
func (d *RodDriver) generateSnapshotLocked(ctx context.Context, interactiveOnly, compact bool, depth int, selector string) (*SnapshotResult, error) {
	pageObj, err := d.getActivePageLocked(ctx)
	if err != nil {
		return nil, err
	}

	page := pageObj.Context(ctx)
	info, _ := page.Info()
	urlStr := ""
	title := ""
	if info != nil {
		urlStr = info.URL
		title = info.Title
	}

	// JavaScript extractor script that collects interactive and content DOM elements
	const extractJS = `(selector, interactiveOnly) => {
		const root = selector ? document.querySelector(selector) : document.body;
		if (!root) return [];

		const isVisible = (el) => {
			const rect = el.getBoundingClientRect();
			const style = window.getComputedStyle(el);
			return style && style.visibility !== 'hidden' && style.display !== 'none' &&
				rect.width > 0 && rect.height > 0;
		};

		const interactiveTags = new Set(['BUTTON', 'A', 'INPUT', 'SELECT', 'TEXTAREA']);
		const interactiveRoles = new Set(['button', 'link', 'checkbox', 'radio', 'tab', 'menuitem', 'combobox', 'textbox', 'searchbox', 'switch']);
		const contentRoles = new Set(['status', 'alert', 'heading', 'article', 'region', 'banner', 'main', 'complementary']);

		const elements = [];
		let idx = 1;

		const processNode = (node, contextPrefix) => {
			if (!isVisible(node)) return;

			const tag = node.tagName ? node.tagName.toUpperCase() : '';
			const role = (node.getAttribute && node.getAttribute('role') || '').toLowerCase();
			const isClickable = node.hasAttribute && (node.hasAttribute('onclick') || node.hasAttribute('tabindex'));
			const isInteractive = interactiveTags.has(tag) || interactiveRoles.has(role) || isClickable;

			if (isInteractive) {
				const ref = '@e' + idx;
				node.setAttribute('data-yummi-ref', ref);

				const root = node.getRootNode();
				const referenced = (node.getAttribute('aria-labelledby') || '').split(/\s+/).filter(Boolean)
					.map(id => root.getElementById?.(id)?.textContent || '').join(' ').trim();
				const labels = Array.from(node.labels || []).map(label => label.textContent || '').join(' ').trim();
				let name = (referenced || node.getAttribute('aria-label') || labels || node.innerText || node.getAttribute('alt') || node.getAttribute('title') || node.getAttribute('placeholder') || '').trim();
				if (name.length > 80) name = name.substring(0, 77) + '...';

				const elInfo = {
					ref: ref,
					tag: tag.toLowerCase(),
					role: role || (tag === 'A' ? 'link' : (tag === 'BUTTON' ? 'button' : (tag === 'INPUT' ? 'input' : tag.toLowerCase()))),
					name: (contextPrefix ? '[' + contextPrefix + '] ' : '') + name,
					type: (node.getAttribute('type') || '').toLowerCase(),
					value: (node.value || '').substring(0, 50),
					placeholder: node.getAttribute('placeholder') || '',
					href: node.getAttribute('href') || '',
					id: node.id || '',
					nameAttr: node.getAttribute('name') || '',
				};
				elements.push(elInfo);
				idx++;
				if (elements.length >= 100) return;
			} else if (!interactiveOnly) {
				const isHeading = /^H[1-6]$/.test(tag);
				const isContent = isHeading || tag === 'P' || tag === 'PRE' || tag === 'LI' || tag === 'TD' || tag === 'TH' || tag === 'BLOCKQUOTE' || tag === 'CODE' || contentRoles.has(role);
				if (isContent) {
					const text = (node.innerText || node.textContent || '').trim();
					if (text) {
						let trimmed = text.length > 120 ? text.substring(0, 117) + '...' : text;
						elements.push({
							ref: '',
							tag: tag.toLowerCase(),
							role: role || (isHeading ? 'heading' : (tag === 'P' ? 'text' : (tag === 'LI' ? 'listitem' : (tag === 'TD' || tag === 'TH' ? 'cell' : tag.toLowerCase())))),
							name: (contextPrefix ? '[' + contextPrefix + '] ' : '') + trimmed,
							type: '',
							value: '',
							placeholder: '',
							href: '',
							id: node.id || '',
							nameAttr: '',
						});
						if (elements.length >= 100) return;
					}
				}
			}

			// Traverse open Shadow DOM
			if (node.shadowRoot) {
				const shadowChildren = node.shadowRoot.children;
				if (shadowChildren) {
					for (let i = 0; i < shadowChildren.length; i++) {
						walk(shadowChildren[i], contextPrefix ? contextPrefix + ' > shadow' : 'shadow');
						if (elements.length >= 100) return;
					}
				}
			}

			// Traverse accessible same-origin iframe
			if (tag === 'IFRAME') {
				try {
					const doc = node.contentDocument || (node.contentWindow && node.contentWindow.document);
					if (doc && doc.body) {
						const frameLabel = node.id || node.name || 'frame';
						const prefix = contextPrefix ? contextPrefix + ' > iframe#' + frameLabel : 'iframe#' + frameLabel;
						const children = doc.body.children;
						if (children) {
							for (let i = 0; i < children.length; i++) {
								walk(children[i], prefix);
								if (elements.length >= 100) return;
							}
						}
					}
				} catch(e) {
					// Cross-origin iframe boundary
					const ref = '@e' + idx;
					elements.push({
						ref: ref,
						tag: 'iframe',
						role: 'iframe',
						name: '[cross-origin iframe: ' + (node.src || 'unspecified') + ']',
						type: '',
						value: '',
						placeholder: '',
						href: node.src || '',
						id: node.id || '',
						nameAttr: node.name || '',
					});
					idx++;
				}
			}
		};

		const walk = (elem, prefix) => {
			if (!elem) return;
			processNode(elem, prefix);
			if (elements.length >= 100) return;
			const children = elem.children;
			if (children) {
				for (let i = 0; i < children.length; i++) {
					walk(children[i], prefix);
					if (elements.length >= 100) return;
				}
			}
		};

		walk(root, '');
		return elements;
	}`

	val, err := page.Eval(extractJS, selector, interactiveOnly)
	if err != nil {
		return nil, fmt.Errorf("eval snapshot extractor: %w", err)
	}

	var rawItems []struct {
		Ref         string `json:"ref"`
		Tag         string `json:"tag"`
		Role        string `json:"role"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		Value       string `json:"value"`
		Placeholder string `json:"placeholder"`
		Href        string `json:"href"`
		ID          string `json:"id"`
		NameAttr    string `json:"nameAttr"`
	}

	rawBytes, _ := json.Marshal(val.Value.Val())
	_ = json.Unmarshal(rawBytes, &rawItems)

	d.currentRefs = make(map[string]*rod.Element)
	d.currentElems = make([]SnapshotElement, 0, len(rawItems))

	var b strings.Builder
	fmt.Fprintf(&b, "Page: %s\nURL: %s\n\n", title, urlStr)

	for _, item := range rawItems {
		if item.Ref != "" {
			snapEl := SnapshotElement{
				Ref:         item.Ref,
				Role:        item.Role,
				Name:        item.Name,
				Type:        item.Type,
				Value:       item.Value,
				Placeholder: item.Placeholder,
				Href:        item.Href,
				Attributes:  map[string]string{},
			}
			if item.ID != "" {
				snapEl.Attributes["id"] = item.ID
			}
			if item.NameAttr != "" {
				snapEl.Attributes["name"] = item.NameAttr
			}

			d.currentElems = append(d.currentElems, snapEl)

			// Render compact line
			b.WriteString(item.Ref + " [" + item.Role + "]")
			if item.ID != "" {
				b.WriteString(` id="` + item.ID + `"`)
			}
			if item.Type != "" {
				b.WriteString(` type="` + item.Type + `"`)
			}
			if item.NameAttr != "" {
				b.WriteString(` name="` + item.NameAttr + `"`)
			}
			if item.Placeholder != "" {
				b.WriteString(` placeholder="` + item.Placeholder + `"`)
			}
			if item.Name != "" {
				b.WriteString(` "` + item.Name + `"`)
			}
			if item.Href != "" {
				b.WriteString(` href="` + item.Href + `"`)
			}
			if item.Value != "" && item.Type != "password" {
				b.WriteString(` value="` + item.Value + `"`)
			}
			b.WriteString("\n")
		} else {
			// Non-interactive structural/content element (when interactiveOnly == false)
			b.WriteString("[" + item.Role + "]")
			if item.ID != "" {
				b.WriteString(` id="` + item.ID + `"`)
			}
			if item.Name != "" {
				b.WriteString(` "` + item.Name + `"`)
			}
			b.WriteString("\n")
		}
	}

	if len(rawItems) == 0 {
		b.WriteString("[no interactive elements detected]\n")
	}

	return &SnapshotResult{
		URL:      urlStr,
		Title:    title,
		TreeText: b.String(),
		Elements: d.currentElems,
	}, nil
}

// Snapshot generates a compact accessibility tree snapshot.
func (d *RodDriver) Snapshot(ctx context.Context, interactiveOnly, compact bool, depth int, selector string) (*SnapshotResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.generateSnapshotLocked(ctx, interactiveOnly, compact, depth, selector)
}

// resolveElement finds an element by ref (@eN) or selector.
func (d *RodDriver) resolveElement(ctx context.Context, ref, selector string) (*rod.Element, error) {
	page := d.activePage.Context(ctx)

	if ref != "" {
		ref = strings.TrimSpace(ref)
		if !strings.HasPrefix(ref, "@e") {
			ref = "@e" + ref
		}

		// If element cache is empty (e.g. fresh MCP process), tag the DOM first
		if len(d.currentElems) == 0 {
			_, _ = d.generateSnapshotLocked(ctx, true, true, 4, "")
		}

		// 1. Direct 1-to-1 match via data-yummi-ref attribute with fast bounded search timeout
		if el, err := page.Timeout(1 * time.Second).Element(fmt.Sprintf(`[data-yummi-ref="%s"]`, ref)); err == nil && el != nil {
			return el.Context(ctx), nil
		}

		// 2. Piercing search across open Shadow DOM on top page
		const deepQueryJS = `(ref) => {
			function findInRoot(root) {
				if (!root) return null;
				let el = root.querySelector('[data-yummi-ref="' + ref + '"]');
				if (el) return el;
				const all = root.querySelectorAll('*');
				for (let i = 0; i < all.length; i++) {
					if (all[i].shadowRoot) {
						el = findInRoot(all[i].shadowRoot);
						if (el) return el;
					}
				}
				return null;
			}
			return findInRoot(document);
		}`
		if el, err := page.Timeout(1 * time.Second).ElementByJS(rod.Eval(deepQueryJS, ref)); err == nil && el != nil {
			return el.Context(ctx), nil
		}

		// 3. Check inside any iframe contexts (including shadow roots within iframes)
		if iframes, err := page.Elements("iframe"); err == nil {
			for _, ifr := range iframes {
				if framePage, fErr := ifr.Frame(); fErr == nil && framePage != nil {
					if el, err := framePage.Timeout(500 * time.Millisecond).Element(fmt.Sprintf(`[data-yummi-ref="%s"]`, ref)); err == nil && el != nil {
						return el.Context(ctx), nil
					}
					if el, err := framePage.Timeout(500 * time.Millisecond).ElementByJS(rod.Eval(deepQueryJS, ref)); err == nil && el != nil {
						return el.Context(ctx), nil
					}
				}
			}
		}

		var targetElem *SnapshotElement
		for i := range d.currentElems {
			if d.currentElems[i].Ref == ref {
				targetElem = &d.currentElems[i]
				break
			}
		}

		if targetElem != nil {
			// Try specific selector matches first
			if id := targetElem.Attributes["id"]; id != "" {
				if el, err := page.Timeout(500 * time.Millisecond).Element("#" + id); err == nil && el != nil {
					return el.Context(ctx), nil
				}
			}
			if name := targetElem.Attributes["name"]; name != "" {
				if el, err := page.Timeout(500 * time.Millisecond).Element(fmt.Sprintf(`[name="%s"]`, name)); err == nil && el != nil {
					return el.Context(ctx), nil
				}
			}
			if targetElem.Placeholder != "" {
				if el, err := page.Timeout(500 * time.Millisecond).Element(fmt.Sprintf(`[placeholder="%s"]`, targetElem.Placeholder)); err == nil && el != nil {
					return el.Context(ctx), nil
				}
			}
			if targetElem.Href != "" {
				if el, err := page.Timeout(500 * time.Millisecond).Element(fmt.Sprintf(`a[href="%s"]`, targetElem.Href)); err == nil && el != nil {
					return el.Context(ctx), nil
				}
			}
		}

		// Fallback: search by text or placeholder
		if targetElem != nil && targetElem.Name != "" {
			if el, err := page.Timeout(500*time.Millisecond).ElementR(targetElem.Role, targetElem.Name); err == nil && el != nil {
				return el.Context(ctx), nil
			}
		}
	}

	if selector != "" {
		if el, err := page.Timeout(2 * time.Second).Element(selector); err == nil && el != nil {
			return el.Context(ctx), nil
		}
	}

	return nil, fmt.Errorf("element %q not found or reference is stale; please run browser_snapshot to refresh refs", ref)
}

// Interact performs an action against an element, viewport, or coordinates.
func (d *RodDriver) Interact(ctx context.Context, p InteractParams) (*InteractResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	pageObj, err := d.getActivePageLocked(ctx)
	if err != nil {
		return nil, err
	}

	page := pageObj.Context(ctx)
	initInfo, _ := page.Info()
	initURL := ""
	if initInfo != nil {
		initURL = initInfo.URL
	}

	act := strings.ToLower(strings.TrimSpace(p.Action))
	res := &InteractResult{
		Action:  act,
		Target:  p.Ref,
		Success: true,
	}
	if res.Target == "" {
		res.Target = p.Selector
	}

	switch act {
	case "click":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		_ = el.Timeout(2 * time.Second).ScrollIntoView()
		if err := el.Timeout(2*time.Second).Click(proto.InputMouseButtonLeft, 1); err != nil {
			if _, evalErr := el.Timeout(2 * time.Second).Eval(`() => this.click()`); evalErr != nil {
				return nil, fmt.Errorf("click failed: %w", err)
			}
		}
		res.Message = "clicked " + res.Target

	case "dblclick":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		_ = el.Timeout(2 * time.Second).ScrollIntoView()
		if err := el.Timeout(2*time.Second).Click(proto.InputMouseButtonLeft, 2); err != nil {
			return nil, fmt.Errorf("double click failed: %w", err)
		}
		res.Message = "double clicked " + res.Target

	case "right_click", "context_menu":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		_ = el.Timeout(2 * time.Second).ScrollIntoView()
		if err := el.Timeout(2*time.Second).Click(proto.InputMouseButtonRight, 1); err != nil {
			return nil, fmt.Errorf("right click failed: %w", err)
		}
		res.Message = "right-clicked " + res.Target

	case "check":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		_ = el.Timeout(2 * time.Second).ScrollIntoView()
		_, _ = el.Timeout(2 * time.Second).Eval(`() => {
			if (!this.checked) {
				this.click();
				this.checked = true;
				this.dispatchEvent(new Event('input', {bubbles: true}));
				this.dispatchEvent(new Event('change', {bubbles: true}));
			}
		}`)
		res.Message = "checked " + res.Target

	case "uncheck":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		_ = el.Timeout(2 * time.Second).ScrollIntoView()
		_, _ = el.Timeout(2 * time.Second).Eval(`() => {
			if (this.checked) {
				this.click();
				this.checked = false;
				this.dispatchEvent(new Event('input', {bubbles: true}));
				this.dispatchEvent(new Event('change', {bubbles: true}));
			}
		}`)
		res.Message = "unchecked " + res.Target

	case "click_at", "mouse_click":
		if err := page.Mouse.MoveTo(proto.Point{X: p.X, Y: p.Y}); err != nil {
			return nil, fmt.Errorf("move to (%v, %v): %w", p.X, p.Y, err)
		}
		if err := page.Mouse.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return nil, fmt.Errorf("click at (%v, %v): %w", p.X, p.Y, err)
		}
		res.Message = fmt.Sprintf("clicked at viewport coordinate (%.1f, %.1f)", p.X, p.Y)

	case "move_to":
		if err := page.Mouse.MoveTo(proto.Point{X: p.X, Y: p.Y}); err != nil {
			return nil, fmt.Errorf("move to (%v, %v): %w", p.X, p.Y, err)
		}
		res.Message = fmt.Sprintf("moved mouse pointer to (%.1f, %.1f)", p.X, p.Y)

	case "drag_and_drop", "drag":
		startX, startY := p.X, p.Y
		endX, endY := p.ToX, p.ToY
		if p.Ref != "" || p.Selector != "" {
			srcEl, err := d.resolveElement(ctx, p.Ref, p.Selector)
			if err != nil {
				return nil, fmt.Errorf("drag source: %w", err)
			}
			_ = srcEl.ScrollIntoView()
			if shape, err := srcEl.Shape(); err == nil && shape != nil {
				if pt := shape.OnePointInside(); pt != nil {
					startX = pt.X
					startY = pt.Y
				}
			}
		}
		if p.ToRef != "" {
			dstEl, err := d.resolveElement(ctx, p.ToRef, "")
			if err != nil {
				return nil, fmt.Errorf("drag destination: %w", err)
			}
			_ = dstEl.ScrollIntoView()
			if shape, err := dstEl.Shape(); err == nil && shape != nil {
				if pt := shape.OnePointInside(); pt != nil {
					endX = pt.X
					endY = pt.Y
				}
			}
		}
		_ = page.Mouse.MoveTo(proto.Point{X: startX, Y: startY})
		_ = page.Mouse.Down(proto.InputMouseButtonLeft, 1)
		_ = page.Mouse.MoveLinear(proto.Point{X: endX, Y: endY}, 10)
		_ = page.Mouse.Up(proto.InputMouseButtonLeft, 1)
		res.Message = fmt.Sprintf("dragged from (%.1f, %.1f) to (%.1f, %.1f)", startX, startY, endX, endY)

	case "set_viewport":
		w := p.Width
		h := p.Height
		if w <= 0 {
			w = 1600
		}
		if h <= 0 {
			h = 900
		}
		isMobile := w <= 600
		override := proto.EmulationSetDeviceMetricsOverride{
			Width:             w,
			Height:            h,
			DeviceScaleFactor: 1.0,
			Mobile:            isMobile,
		}
		if err := override.Call(page); err != nil {
			return nil, fmt.Errorf("set_viewport (%dx%d): %w", w, h, err)
		}
		res.Message = fmt.Sprintf("viewport set to %dx%d (mobile=%v)", w, h, isMobile)

	case "clipboard_read":
		val, err := page.Timeout(1 * time.Second).Eval(`() => {
			try {
				return navigator.clipboard.readText();
			} catch(e) {
				return '';
			}
		}`)
		if err == nil && val != nil && val.Value.Str() != "" {
			res.Message = val.Value.Str()
		} else if out, execErr := os.ReadFile("/tmp/yummi_clipboard.txt"); execErr == nil {
			res.Message = string(out)
		} else {
			res.Message = ""
		}

	case "clipboard_write":
		_, _ = page.Timeout(1*time.Second).Eval(`(text) => {
			try {
				return navigator.clipboard.writeText(text);
			} catch(e) {}
		}`, p.Value)
		_ = os.WriteFile("/tmp/yummi_clipboard.txt", []byte(p.Value), 0o644)
		res.Message = fmt.Sprintf("copied %q to clipboard", p.Value)

	case "fill":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		_ = el.Timeout(1 * time.Second).ScrollIntoView()
		_ = el.Timeout(1 * time.Second).Focus()
		// Set value directly via JavaScript and dispatch input/change events for full reactive support
		_, evalErr := el.Timeout(2*time.Second).Eval(`(val) => {
			this.focus();
			this.value = val;
			this.dispatchEvent(new Event('input', {bubbles: true, cancelable: true}));
			this.dispatchEvent(new Event('change', {bubbles: true, cancelable: true}));
		}`, p.Value)
		if evalErr != nil {
			// Fallback: clear and type
			_, _ = el.Timeout(1 * time.Second).Eval(`() => { this.value = ''; }`)
			if typeErr := el.Timeout(3 * time.Second).Input(p.Value); typeErr != nil {
				return nil, fmt.Errorf("fill input failed: %w", evalErr)
			}
		}
		res.Message = fmt.Sprintf("filled %s with %q", res.Target, p.Value)

	case "type":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		_ = el.Timeout(1 * time.Second).Focus()
		if err := el.Timeout(3 * time.Second).Input(p.Value); err != nil {
			return nil, fmt.Errorf("type failed: %w", err)
		}
		res.Message = fmt.Sprintf("typed %q into %s", p.Value, res.Target)

	case "press":
		if p.Ref != "" || p.Selector != "" {
			if el, err := d.resolveElement(ctx, p.Ref, p.Selector); err == nil && el != nil {
				_ = el.Timeout(1 * time.Second).Focus()
			}
		}
		key := strings.ToLower(strings.TrimSpace(p.Value))
		switch key {
		case "enter":
			_ = page.Keyboard.Press(input.Enter)
		case "tab":
			_ = page.Keyboard.Press(input.Tab)
		case "escape", "esc":
			_ = page.Keyboard.Press(input.Escape)
		case "backspace":
			_ = page.Keyboard.Press(input.Backspace)
		case "delete":
			_ = page.Keyboard.Press(input.Delete)
		case "space":
			_ = page.Keyboard.Press(input.Space)
		case "arrowdown", "down":
			_ = page.Keyboard.Press(input.ArrowDown)
		case "arrowup", "up":
			_ = page.Keyboard.Press(input.ArrowUp)
		case "arrowleft", "left":
			_ = page.Keyboard.Press(input.ArrowLeft)
		case "arrowright", "right":
			_ = page.Keyboard.Press(input.ArrowRight)
		case "ctrl+a", "control+a":
			_, _ = page.Timeout(1 * time.Second).Eval(`() => document.execCommand('selectAll')`)
		default:
			_ = page.InsertText(p.Value)
		}
		res.Message = "pressed key " + p.Value

	case "hover":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		_ = el.Hover()
		res.Message = "hovered over " + res.Target

	case "select":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		if err := el.Select([]string{p.Value}, true, rod.SelectorTypeRegex); err != nil {
			return nil, fmt.Errorf("select option failed: %w", err)
		}
		res.Message = fmt.Sprintf("selected option %q in %s", p.Value, res.Target)

	case "upload":
		el, err := d.resolveElement(ctx, p.Ref, p.Selector)
		if err != nil {
			return nil, err
		}
		cleanFile := filepath.Clean(p.Value)
		if !filepath.IsAbs(cleanFile) {
			return nil, fmt.Errorf("upload path must be absolute: %s", p.Value)
		}
		if err := el.SetFiles([]string{cleanFile}); err != nil {
			return nil, fmt.Errorf("file upload failed: %w", err)
		}
		res.Message = fmt.Sprintf("uploaded file %s to %s", cleanFile, res.Target)

	case "scroll":
		amount := p.ScrollAmount
		if amount <= 0 {
			amount = 500
		}
		switch strings.ToLower(p.ScrollDir) {
		case "up":
			_ = page.Mouse.Scroll(0, float64(-amount), 5)
		case "down":
			_ = page.Mouse.Scroll(0, float64(amount), 5)
		default:
			_ = page.Mouse.Scroll(0, float64(amount), 5)
		}
		res.Message = fmt.Sprintf("scrolled %s by %d px", p.ScrollDir, amount)

	case "scroll_to":
		if p.Ref != "" || p.Selector != "" {
			el, err := d.resolveElement(ctx, p.Ref, p.Selector)
			if err != nil {
				return nil, err
			}
			_ = el.Timeout(2 * time.Second).ScrollIntoView()
			res.Message = "scrolled element " + res.Target + " into view"
		} else {
			_ = page.Mouse.Scroll(p.X, p.Y, 5)
			res.Message = fmt.Sprintf("scrolled viewport to (%.1f, %.1f)", p.X, p.Y)
		}

	case "wait_for_selector":
		targetSel := p.Selector
		if targetSel == "" && p.Ref != "" {
			targetSel = fmt.Sprintf(`[data-yummi-ref="%s"]`, p.Ref)
		}
		if targetSel == "" {
			return nil, fmt.Errorf("selector is required for wait_for_selector")
		}
		dur := 5 * time.Second
		if p.Duration > 0 {
			dur = time.Duration(p.Duration * float64(time.Second))
		}
		if _, err := page.Timeout(dur).Element(targetSel); err != nil {
			return nil, fmt.Errorf("timeout waiting for selector %s: %w", targetSel, err)
		}
		res.Message = "waited for selector " + targetSel

	case "wait", "sleep":
		dur := 2 * time.Second
		if p.Duration > 0 {
			dur = time.Duration(p.Duration * float64(time.Second))
		}
		if dur > 30*time.Second {
			return nil, fmt.Errorf("wait duration exceeds 30 seconds")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(dur):
		}
		res.Message = fmt.Sprintf("waited for %v", dur)

	default:
		return nil, fmt.Errorf("unsupported interaction action: %s", p.Action)
	}

	// Short wait for any resulting DOM or navigation update
	time.Sleep(300 * time.Millisecond)
	curInfo, _ := page.Info()
	if curInfo != nil {
		res.CurrentURL = curInfo.URL
		res.CurrentTitle = curInfo.Title
		if initURL != "" && curInfo.URL != initURL {
			res.Navigated = true
		}
	}

	return res, nil
}

// Eval runs JavaScript in page context.
func (d *RodDriver) Eval(ctx context.Context, script string) (any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	pageObj, err := d.getActivePageLocked(ctx)
	if err != nil {
		return nil, err
	}

	req := proto.RuntimeEvaluate{
		Expression:    script,
		AwaitPromise:  true,
		ReturnByValue: true,
		UserGesture:   true,
	}
	res, err := req.Call(pageObj.Context(ctx))
	if err != nil {
		return nil, fmt.Errorf("eval failed: %w", err)
	}
	if res.ExceptionDetails != nil {
		errDesc := res.ExceptionDetails.Text
		if res.ExceptionDetails.Exception != nil && res.ExceptionDetails.Exception.Description != "" {
			errDesc = res.ExceptionDetails.Exception.Description
		}
		return nil, fmt.Errorf("eval js error: %s", errDesc)
	}
	if res.Result == nil || res.Result.Value.Val() == nil {
		if res.Result != nil && res.Result.Description != "" {
			return res.Result.Description, nil
		}
		return nil, nil
	}
	return res.Result.Value.Val(), nil
}

// Screenshot captures a PNG screenshot.
func (d *RodDriver) Screenshot(ctx context.Context, fullPage, annotate bool, outputFile string) (*ScreenshotResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	pageObj, err := d.getActivePageLocked(ctx)
	if err != nil {
		return nil, err
	}

	out := strings.TrimSpace(outputFile)
	if out == "" {
		dir := "/home/pentester/work/screenshots"
		_ = os.MkdirAll(dir, 0o755)
		out = filepath.Join(dir, fmt.Sprintf("shot_%d.png", time.Now().UnixNano()))
	}
	clean, reason := confineDir(filepath.Dir(out))
	if reason != "" {
		return nil, fmt.Errorf("output directory %s: %s", filepath.Dir(out), reason)
	}
	targetPath := filepath.Join(clean, filepath.Base(out))

	page := pageObj.Context(ctx)
	var imgBytes []byte

	if annotate {
		// Set-of-Marks: inject visual numbered badges over interactive [data-yummi-ref] elements
		const somScript = `(() => {
			document.getElementById('yummi-som-overlay')?.remove();
			const container = document.createElement('div');
			container.id = 'yummi-som-overlay';
			container.style.cssText = 'position: absolute; top: 0; left: 0; width: 100%; height: 100%; pointer-events: none; z-index: 2147483647;';
			document.body.appendChild(container);

			const markElement = (el, ref) => {
				const rect = el.getBoundingClientRect();
				if (rect.width <= 0 || rect.height <= 0) return;

				const tag = el.tagName.toUpperCase();
				let color = '#ef4444'; // Red default
				if (tag === 'BUTTON' || el.getAttribute('role') === 'button') color = '#3b82f6'; // Blue for buttons
				else if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') color = '#10b981'; // Green for inputs
				else if (tag === 'A' || el.getAttribute('role') === 'link') color = '#f59e0b'; // Amber for links

				const box = document.createElement('div');
				box.style.cssText = 'position: absolute; left: ' + (rect.left + window.scrollX) + 'px; top: ' + (rect.top + window.scrollY) + 'px; width: ' + rect.width + 'px; height: ' + rect.height + 'px; border: 2px solid ' + color + '; box-sizing: border-box; pointer-events: none;';

				const pill = document.createElement('span');
				pill.innerText = ref;
				pill.style.cssText = 'position: absolute; left: 0; top: 0; background: ' + color + '; color: #ffffff; font-family: monospace; font-size: 11px; font-weight: bold; padding: 1px 4px; border-radius: 2px; box-shadow: 0 1px 3px rgba(0,0,0,0.5); white-space: nowrap; pointer-events: none;';
				box.appendChild(pill);
				container.appendChild(box);
			};

			const collectRefs = (root) => {
				if (!root) return;
				const els = root.querySelectorAll ? root.querySelectorAll('[data-yummi-ref]') : [];
				els.forEach(el => markElement(el, el.getAttribute('data-yummi-ref')));
				const all = root.querySelectorAll ? root.querySelectorAll('*') : [];
				all.forEach(node => {
					if (node.shadowRoot) collectRefs(node.shadowRoot);
					if (node.tagName === 'IFRAME') {
						try {
							const doc = node.contentDocument || (node.contentWindow && node.contentWindow.document);
							if (doc) collectRefs(doc);
						} catch(e) {}
					}
				});
			};

			collectRefs(document);
		})()`
		_, _ = page.Timeout(1 * time.Second).Eval(somScript)
	}

	if fullPage {
		imgBytes, err = page.Screenshot(true, nil)
	} else {
		imgBytes, err = page.Screenshot(false, nil)
	}

	if annotate {
		_, _ = page.Timeout(1 * time.Second).Eval(`(() => { document.getElementById('yummi-som-overlay')?.remove(); })()`)
	}

	if err != nil {
		return nil, fmt.Errorf("screenshot capture failed: %w", err)
	}

	if _, reason := writeConfinedFile(targetPath, imgBytes); reason != "" {
		return nil, fmt.Errorf("write screenshot file: %s", reason)
	}

	return &ScreenshotResult{
		FilePath:  targetPath,
		Width:     screenshotWidth(imgBytes),
		Height:    screenshotHeight(imgBytes),
		Annotated: annotate,
	}, nil
}

// Storage manages cookies, localStorage, and session state.
func (d *RodDriver) Storage(ctx context.Context, p StorageParams) (*StorageResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	pageObj, err := d.getActivePageLocked(ctx)
	if err != nil {
		return nil, err
	}

	page := pageObj.Context(ctx)
	act := strings.ToLower(strings.TrimSpace(p.Action))

	switch act {
	case "get_cookies":
		ckList, err := page.Cookies(nil)
		if err != nil {
			return nil, fmt.Errorf("get cookies failed: %w", err)
		}
		var formatted []map[string]any
		for _, c := range ckList {
			formatted = append(formatted, map[string]any{
				"name":     c.Name,
				"value":    c.Value,
				"domain":   c.Domain,
				"path":     c.Path,
				"httpOnly": c.HTTPOnly,
				"secure":   c.Secure,
			})
		}
		return &StorageResult{
			Action:  act,
			Cookies: formatted,
			Message: fmt.Sprintf("retrieved %d cookies", len(formatted)),
		}, nil

	case "set_cookie":
		cookieName := strings.TrimSpace(p.Name)
		cookieVal := strings.TrimSpace(p.Value)
		if cookieName == "" {
			return nil, fmt.Errorf("cookie name is required for set_cookie")
		}
		curInfo, _ := page.Info()
		cookieDomain := p.Domain
		cookieURL := ""
		if curInfo != nil && curInfo.URL != "" {
			cookieURL = curInfo.URL
			if cookieDomain == "" {
				if parsedU, err := url.Parse(curInfo.URL); err == nil {
					cookieDomain = parsedU.Hostname()
				}
			}
		}
		setReq := proto.NetworkSetCookie{
			Name:  cookieName,
			Value: cookieVal,
			Path:  "/",
		}
		if cookieURL != "" {
			setReq.URL = cookieURL
		}
		if cookieDomain != "" && cookieDomain != "localhost" && cookieDomain != "127.0.0.1" {
			setReq.Domain = cookieDomain
		}
		if _, err := setReq.Call(page); err != nil {
			// Fallback: document.cookie eval
			_, evalErr := page.Eval(`(name, val) => { document.cookie = name + "=" + encodeURIComponent(val) + "; path=/"; }`, cookieName, cookieVal)
			if evalErr != nil {
				return nil, fmt.Errorf("set cookie failed: %w", err)
			}
		}
		return &StorageResult{
			Action:  act,
			Message: fmt.Sprintf("set cookie %s=%q", cookieName, cookieVal),
		}, nil

	case "get_storage":
		res, err := page.Timeout(2 * time.Second).Eval(`() => {
			return {
				localStorage: Object.fromEntries(Object.entries(window.localStorage)),
				sessionStorage: Object.fromEntries(Object.entries(window.sessionStorage))
			};
		}`)
		if err != nil {
			return nil, fmt.Errorf("get_storage failed: %w", err)
		}
		rawBytes, _ := json.Marshal(res.Value.Val())
		var storeData map[string]map[string]string
		_ = json.Unmarshal(rawBytes, &storeData)
		return &StorageResult{
			Action:  act,
			Storage: storeData,
			Message: fmt.Sprintf("retrieved %d localStorage and %d sessionStorage items", len(storeData["localStorage"]), len(storeData["sessionStorage"])),
		}, nil

	case "clear":
		_ = proto.NetworkClearBrowserCookies{}.Call(page)
		_, _ = page.Timeout(1 * time.Second).Eval(`() => {
			window.localStorage.clear();
			window.sessionStorage.clear();
		}`)
		return &StorageResult{
			Action:  act,
			Message: "cleared all cookies, localStorage, and sessionStorage",
		}, nil

	case "save_state":
		savePath := p.Path
		if savePath == "" {
			savePath = "/home/pentester/work/auth_state.json"
		}
		clean, reason := confineDir(filepath.Dir(savePath))
		if reason != "" {
			return nil, fmt.Errorf("save_state dir: %s", reason)
		}
		fullSavePath := filepath.Join(clean, filepath.Base(savePath))

		ckList, _ := page.Cookies(nil)
		localData, _ := page.Eval(`() => JSON.stringify(window.localStorage)`)

		state := map[string]any{
			"timestamp":    time.Now().Format(time.RFC3339),
			"cookies":      ckList,
			"localStorage": localData.Value.Str(),
		}
		b, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return nil, err
		}
		if _, reason := writeConfinedFile(fullSavePath, b); reason != "" {
			return nil, fmt.Errorf("write auth state: %s", reason)
		}
		return &StorageResult{
			Action:  act,
			Path:    fullSavePath,
			Message: "saved auth state to " + fullSavePath,
		}, nil

	case "load_state":
		clean, reason := confinedBrowserRead(p.Path)
		if reason != "" {
			return nil, fmt.Errorf("load_state path: %s", reason)
		}
		data, err := os.ReadFile(clean)
		if err != nil {
			return nil, fmt.Errorf("read auth state: %w", err)
		}
		var state struct {
			Cookies      []*proto.NetworkCookieParam `json:"cookies"`
			LocalStorage string                      `json:"localStorage"`
		}
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, fmt.Errorf("invalid state json: %w", err)
		}
		if len(state.Cookies) > 0 {
			_ = page.SetCookies(state.Cookies)
		}
		if state.LocalStorage != "" {
			_, _ = page.Eval(`(data) => {
				try {
					const obj = JSON.parse(data);
					for (const k in obj) { window.localStorage.setItem(k, obj[k]); }
				} catch(e) {}
			}`, state.LocalStorage)
		}
		return &StorageResult{
			Action:  act,
			Path:    clean,
			Message: "loaded auth state from " + clean,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported storage action: %s", p.Action)
	}
}

// Tabs manages multiple browser tabs.
func (d *RodDriver) Tabs(ctx context.Context, action string, tabID int, urlStr string) (*TabsResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.browser == nil {
		if err := d.ensureBrowser(ctx, nil); err != nil {
			return nil, err
		}
	}

	activeBrowser, err := d.activeBrowserLocked()
	if err != nil {
		return nil, err
	}
	pages, err := sessionPages(ctx, activeBrowser)
	if err != nil {
		return nil, fmt.Errorf("failed to get pages: %w", err)
	}

	act := strings.ToLower(strings.TrimSpace(action))
	res := &TabsResult{
		Action: act,
	}

	switch act {
	case "list":
		for i, p := range pages {
			inf, _ := p.Info()
			u, t := "", ""
			if inf != nil {
				u = inf.URL
				t = inf.Title
			}
			res.Tabs = append(res.Tabs, TabItem{
				ID:       i + 1,
				Title:    t,
				URL:      u,
				IsActive: p == d.activePage,
			})
		}
		return res, nil

	case "new":
		newPage, err := stealth.Page(activeBrowser)
		if err != nil {
			newPage, err = activeBrowser.Page(proto.TargetCreateTarget{URL: ""})
			if err != nil {
				return nil, err
			}
		}
		if urlStr != "" {
			if err := newPage.Context(ctx).Navigate(urlStr); err != nil {
				_ = newPage.Close()
				return nil, err
			}
		}
		d.activePage = newPage
		d.hookPageEvents(newPage, d.activeSession)
		d.currentRefs = make(map[string]*rod.Element)
		d.currentElems = nil
		res.ActiveTab = len(pages) + 1
		return res, nil

	case "switch":
		if tabID < 1 || tabID > len(pages) {
			return nil, fmt.Errorf("invalid tab id %d (have %d tabs)", tabID, len(pages))
		}
		d.activePage = pages[tabID-1]
		d.currentRefs = make(map[string]*rod.Element)
		d.currentElems = nil
		res.ActiveTab = tabID
		return res, nil

	case "close":
		if tabID >= 1 && tabID <= len(pages) {
			_ = pages[tabID-1].Close()
		} else if d.activePage != nil {
			_ = d.activePage.Close()
		}
		newPages, _ := sessionPages(ctx, activeBrowser)
		if len(newPages) > 0 {
			d.activePage = newPages[0]
		} else {
			d.activePage = nil
		}
		return res, nil

	default:
		return nil, fmt.Errorf("unsupported tab action: %s", action)
	}
}

// Logs returns buffered console messages and errors.
func (d *RodDriver) Logs(ctx context.Context, filter string, clear bool) (*LogsResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	session := d.activeSession
	if session == "" {
		session = "default"
	}
	flt := strings.ToLower(strings.TrimSpace(filter))
	var filtered []LogEntry
	if d.logsBuffer != nil {
		for _, entry := range d.logsBuffer[session] {
			if flt == "" || strings.Contains(strings.ToLower(entry.Type), flt) || strings.Contains(strings.ToLower(entry.Text), flt) {
				filtered = append(filtered, entry)
			}
		}
		if clear {
			d.logsBuffer[session] = nil
		}
	}

	return &LogsResult{
		TotalEntries: len(filtered),
		Entries:      filtered,
	}, nil
}

// Close closes browser and cleans state.
func (d *RodDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logsBuffer = make(map[string][]LogEntry)
	d.logGeneration++
	if d.connectionProxy != nil {
		_ = d.connectionProxy.Close()
		d.connectionProxy = nil
	}
	if d.browser != nil {
		if d.requestRouter != nil {
			_ = d.requestRouter.Stop()
			d.requestRouter = nil
		}
		err := d.browser.Close()
		d.browser = nil
		d.activePage = nil
		d.currentRefs = nil
		d.currentElems = nil
		d.contexts = make(map[string]*rod.Browser)
		d.activeSession = ""
		return err
	}
	return nil
}

func screenshotWidth(b []byte) int {
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0
	}
	return cfg.Width
}
func screenshotHeight(b []byte) int {
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return 0
	}
	return cfg.Height
}

func confinedBrowserRead(path string) (string, string) {
	if !filepath.IsAbs(path) {
		return "", "absolute path required"
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "file unavailable"
	}
	home, err := filepath.EvalSymlinks(sandboxHome)
	if err != nil || !hasPathPrefix(real, home) {
		return "", "file must remain inside sandbox home"
	}
	return real, ""
}

func (d *RodDriver) Session(ctx context.Context, action string) (map[string]any, error) {
	if err := requireSandboxRuntime(); err != nil {
		return nil, err
	}
	switch action {
	case "reset":
		if err := d.Close(); err != nil {
			return nil, err
		}
		return map[string]any{"running": false, "reset": true}, nil
	case "status":
		d.mu.Lock()
		defer d.mu.Unlock()
		labels := make([]string, 0, len(d.contexts))
		for name := range d.contexts {
			labels = append(labels, name)
		}
		return map[string]any{"running": d.browser != nil, "headless": d.headless, "active_session": d.activeSession, "sessions": labels, "environment": "sandbox"}, nil
	default:
		return nil, fmt.Errorf("browser_session action must be status or reset")
	}
}
