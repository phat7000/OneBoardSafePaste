package main

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/systray"
	"github.com/gonutz/ico"
	"github.com/micmonay/keybd_event"
	"github.com/nfnt/resize"
	"github.com/wailsapp/wails/v2/pkg/menu"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// JWT / UUID patterns (RE2) and replacements — single source of truth for defaultRules + upgradeRules.
// All JWT patterns use raw string literals (backticks) so Go does not reinterpret backslashes.
const (
	// (?s) dot-all. {1,2} allows unsigned tokens (header.payload only) and standard 3-part JWTs.
	rulePatternJWT = `(?s)eyJ[a-zA-Z0-9\-_+/=]+(?:\.[a-zA-Z0-9\-_+/=]+){1,2}`
	// Same structure as rulePatternJWT for ReplaceAll fallback (charset parity with jwtCoreRegexp).
	rulePatternJWTFallbackReplace = `(?s)eyJ[A-Za-z0-9\-_+/=]+(?:\.[A-Za-z0-9\-_+/=]+){1,2}`
	rulePatternUUID = `\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`
	ruleReplJWT     = "[JWT]"
	ruleReplUUID    = "[UUID]"
)

const jwtLogMatchMaxLen = 56

// jwtCoreRegexp is the canonical JWT detector for probes and std-jwt application (same as rulePatternJWT).
var jwtCoreRegexp = regexp.MustCompile(rulePatternJWT)

// jwtFallbackReplaceRegexp is used when FindAllStringIndex finds nothing but "eyJ" is still present.
var jwtFallbackReplaceRegexp = regexp.MustCompile(rulePatternJWTFallbackReplace)

type RedactionRule struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Pattern     string `json:"pattern"`
	IsEnabled   bool   `json:"isEnabled"`
	IsLiteral   bool   `json:"isLiteral"`  // True = Exact text match; False = Regex
	Category    string `json:"category"`   // "Cloud & Auth", "Network", "Sensitive Data", "System", "Custom"
	Source      string `json:"source"`     // "Built-In" | "Custom"
	Severity    string `json:"severity"`   // "Critical", "High", "Medium" — risk level
	Description string `json:"description"`
	Replacement string `json:"replacement"` // e.g., "[IPv4]", "[SECRET]"
}

// RedactionEvent describes one redaction applied (for debug mode).
type RedactionEvent struct {
	RuleName    string `json:"ruleName"`
	OriginalText string `json:"originalText"`
	Replacement string `json:"replacement"`
	StartIndex  int    `json:"startIndex"` // in final redacted text
	EndIndex    int    `json:"endIndex"`
}

// RedactResult is the return type of RedactText (Wails: RedactText(input string) RedactResult — not Redact(string) string).
// Ghost Mode uses result.RedactedText when updating the clipboard after startClipboardWatcher calls RedactText.
type RedactResult struct {
	RedactedText string            `json:"redactedText"`
	Events       []RedactionEvent   `json:"events"`
}

var errSettingsNotFound = errors.New("settings.json not found")

// ShowNotificationEvent is emitted to trigger a desktop notification (e.g. "Sensitive Data Redacted").
const ShowNotificationEvent = "show-notification"

// GhostModeSetEvent is emitted when Ghost Mode is turned off from the tray (so the frontend can sync its toggle and storage).
const GhostModeSetEvent = "ghost-mode-set"

// TrayRequestShowEvent: user chose "Show OneBoard Safe Paste" in the system tray. Handled in the frontend so
// SetGhostModeEnabled/WindowShow run on the WebView thread (tray runs on a different goroutine; Win32/WebView2 are not reliable otherwise).
const TrayRequestShowEvent = "tray-request-show"

// TrayRequestQuitEvent: user chose Quit from the tray; frontend calls runtime.Quit (Wails expects quit on the UI thread).
const TrayRequestQuitEvent = "tray-request-quit"

// clipboardWatcherInterval is how often the clipboard is polled when Ghost Mode is active.
const clipboardWatcherInterval = 250 * time.Millisecond

// appForTray holds the app instance so tray menu callbacks can show/quit the window.
var appForTray *App

// trayMu guards trayGhostStatusItem so UpdateTrayMenu is safe before tray is ready.
var trayMu sync.Mutex
var trayGhostStatusItem *systray.MenuItem

// quittingFromTray is set when the user chooses Quit from the tray so BeforeClose skips the dialog.
var quittingFromTrayMu sync.Mutex
var quittingFromTray bool

// trayIconPNG is the OneBoard system tray icon. Converted to ICO for Windows tray.
//go:embed frontend/src/assets/images/OneBoardSafePaste.png
var trayIconPNG []byte

type App struct {
	ctx context.Context
	Rules []RedactionRule
	// saved window size when exiting satellite mode (not bound to frontend)
	savedWindowW, savedWindowH int

	// Ghost Mode: background clipboard monitoring
	ghostMu               sync.Mutex
	ghostModeEnabled      bool
	ghostWindowSentToTray bool // true while main UI is hidden to tray; clipboard redaction can repop WebView2 hit targets
	lastClipboardText     string
	ghostWatcherOnce      sync.Once
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

const clipboardWipeDelay = 30 * time.Second
const clipboardClearedPlaceholder = "[OneBoard Safe Paste: Clipboard Cleared]"

// SetGhostModeEnabled turns Ghost Mode (background clipboard monitoring) on or off.
// When on: window is hidden (minimize to tray). When off: window is shown.
// Call from the UI when the user toggles Ghost — not for cold-start restore (use RestoreGhostModeFromSettings).
func (a *App) SetGhostModeEnabled(enabled bool) {
	debugLog("[Ghost] SetGhostModeEnabled(%v)\n", enabled)
	a.ghostMu.Lock()
	a.ghostModeEnabled = enabled
	if enabled {
		a.ghostWindowSentToTray = true
	} else {
		a.ghostWindowSentToTray = false
	}
	a.ghostMu.Unlock()
	if a.ctx != nil {
		if enabled {
			wailsruntime.WindowHide(a.ctx)
			ghostFixWebViewHostVisibility(false)
		} else {
			wailsruntime.WindowShow(a.ctx)
			ghostFixWebViewHostVisibility(true)
		}
	}
	UpdateTrayMenu()
}

// GhostHideToTray hides the main window when the user closes it while Ghost Mode is already on.
// Must match SetGhostModeEnabled(true) (Wails does not hide WebView2 children by itself).
func (a *App) GhostHideToTray() {
	if a.ctx == nil {
		return
	}
	a.ghostMu.Lock()
	a.ghostWindowSentToTray = true
	a.ghostMu.Unlock()
	wailsruntime.WindowHide(a.ctx)
	ghostFixWebViewHostVisibility(false)
}

// RestoreGhostModeFromSettings applies saved Ghost Mode monitoring state after launch without hiding
// the window. Hiding only when the user turns Ghost on avoids “app won’t open” after a previous tray session.
func (a *App) RestoreGhostModeFromSettings(enabled bool) {
	debugLog("[Ghost] RestoreGhostModeFromSettings(%v)\n", enabled)
	a.ghostMu.Lock()
	a.ghostModeEnabled = enabled
	a.ghostWindowSentToTray = false
	a.ghostMu.Unlock()
	UpdateTrayMenu()
	if a.ctx != nil {
		wailsruntime.WindowShow(a.ctx)
		ghostFixWebViewHostVisibility(true)
	}
}

// UpdateTrayMenu updates the system tray menu and tooltip to reflect the current Ghost Mode state.
// Safe to call from any goroutine; no-ops if the tray is not ready yet.
func UpdateTrayMenu() {
	trayMu.Lock()
	m := trayGhostStatusItem
	trayMu.Unlock()
	if m == nil {
		return
	}
	enabled := false
	if appForTray != nil {
		appForTray.ghostMu.Lock()
		enabled = appForTray.ghostModeEnabled
		appForTray.ghostMu.Unlock()
	}
	if enabled {
		m.SetTitle("Ghost Mode: ACTIVE")
		m.Disable()
		m.Check()
		systray.SetTooltip("OneBoard Safe Paste - Protecting Clipboard (right-click for menu)")
	} else {
		m.SetTitle("Ghost Mode: INACTIVE")
		m.Disable()
		m.Uncheck()
		systray.SetTooltip("OneBoard Safe Paste — Right-click for menu")
	}
}

// UpdateClipboard updates lastClipboardText first (before the next 250ms tick), then writes
// sanitized text to the system clipboard for infinite loop protection.
func (a *App) UpdateClipboard(sanitizedText string) {
	a.ghostMu.Lock()
	a.lastClipboardText = sanitizedText
	a.ghostMu.Unlock()
	if !ghostPollWriteClipboard(sanitizedText) {
		debugLog("[Ghost] UpdateClipboard: clipboard busy or write failed\n")
		return
	}
}

// EmitShowNotification triggers a desktop notification. Frontend listens for "show-notification" and shows OS toast.
func (a *App) EmitShowNotification(message string) {
	if a.ctx != nil && message != "" {
		wailsruntime.EventsEmit(a.ctx, ShowNotificationEvent, message)
	}
}

// StartGhostWatcher starts the clipboard watcher for Ghost Mode. Call from the frontend only after rules have loaded successfully.
func (a *App) StartGhostWatcher() {
	a.ghostWatcherOnce.Do(func() {
		go a.startClipboardWatcher()
	})
}

// startClipboardWatcher runs in a goroutine and polls the clipboard every 250ms.
// When Ghost Mode is on and content changed: redact in Go (no frontend dependency), update clipboard, then notify.
func (a *App) startClipboardWatcher() {
	ticker := time.NewTicker(clipboardWatcherInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.ghostMu.Lock()
			enabled := a.ghostModeEnabled
			last := a.lastClipboardText
			a.ghostMu.Unlock()
			if !enabled {
				continue
			}
			current, ok := ghostPollReadClipboard()
			if !ok {
				continue
			}
			if strings.TrimSpace(current) == "" {
				continue
			}
			if current == last {
				continue
			}
			// Redact in Go so it works when the window is hidden (no Svelte dependency).
			result := a.RedactText(current, false)
			redacted := result.RedactedText
			if redacted == current {
				a.ghostMu.Lock()
				a.lastClipboardText = current
				a.ghostMu.Unlock()
				continue
			}
			// Update lastClipboardText before writing so the next tick does not re-detect.
			a.ghostMu.Lock()
			a.lastClipboardText = redacted
			a.ghostMu.Unlock()
			if !ghostPollWriteClipboard(redacted) {
				continue
			}
			if a.ctx != nil {
				wailsruntime.EventsEmit(a.ctx, ShowNotificationEvent, "Sensitive Data Redacted")
			}
			// Clipboard write + JS notification can redraw WebView2; Chrome_WidgetWin may accept hits again over the taskbar.
			a.ghostMu.Lock()
			trayHidden := a.ghostWindowSentToTray
			a.ghostMu.Unlock()
			if trayHidden {
				ghostFixWebViewHostVisibility(false)
			}
		}
	}
}

// StartClipboardTimer starts a 30-second timer. When it fires, if the clipboard still
// contains expectedText, it is cleared (replaced with a placeholder) so the user does
// not leave redacted content on the clipboard. Run in a goroutine; call after copying.
func (a *App) StartClipboardTimer(expectedText string) {
	if expectedText == "" {
		return
	}
	go func() {
		expected := normalizeClipboardText(expectedText)
		<-time.After(clipboardWipeDelay)
		current, ok := ghostPollReadClipboard()
		if !ok {
			return
		}
		if normalizeClipboardText(current) == expected {
			ghostPollWriteClipboard(clipboardClearedPlaceholder)
		}
	}()
}

func normalizeClipboardText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Create or load rules first so the file exists before the frontend calls LoadRules().
	rules, err := a.LoadRules()
	if err == nil {
		a.Rules = rules
	} else {
		a.Rules = defaultRules()
		_ = a.SaveRules(a.Rules)
	}

	// Application menu: product submenu with Show (restore window after Ghost hide) and Quit.
	siloSub := menu.NewMenu()
	siloSub.Append(menu.Text("Show OneBoard Safe Paste", nil, func(*menu.CallbackData) {
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, TrayRequestShowEvent)
		}
	}))
	siloSub.Append(menu.Separator())
	siloSub.Append(menu.Text("Quit", nil, func(*menu.CallbackData) {
		if a.ctx != nil {
			wailsruntime.Quit(a.ctx)
		}
	}))
	appMenu := menu.NewMenu()
	appMenu.Append(menu.SubMenu("OneBoard Safe Paste", siloSub))
	wailsruntime.MenuSetApplicationMenu(ctx, appMenu)

	// System tray: icon in notification area so user can restore or quit when window is hidden (Ghost Mode).
	appForTray = a
	go runTray()
	// Clipboard watcher is started by the frontend after rules load (StartGhostWatcher).
}

// BeforeClose runs when the window is about to close. Always allow quit (no dialog).
func (a *App) BeforeClose(ctx context.Context) bool {
	quittingFromTrayMu.Lock()
	quittingFromTray = false
	quittingFromTrayMu.Unlock()
	return false // allow close/quit
}

// runTray runs the system tray (blocking). Call from a goroutine in startup.
func runTray() {
	systray.Run(trayOnReady, trayOnExit)
}

// pngToTrayICO converts PNG bytes to ICO bytes (16x16) for Windows system tray.
func pngToTrayICO(pngData []byte) ([]byte, error) {
	if len(pngData) == 0 {
		return nil, fmt.Errorf("empty icon data")
	}
	img, _, err := image.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, err
	}
	const size = 16
	small := resize.Resize(size, size, img, resize.Lanczos3)
	icoData, err := ico.FromImage(small)
	if err != nil {
		return nil, err
	}
	return icoData, nil
}

func trayOnReady() {
	// Windows tray requires ICO; PNG often shows blank. Convert once at startup.
	if len(trayIconPNG) > 0 {
		if icoData, err := pngToTrayICO(trayIconPNG); err == nil {
			systray.SetIcon(icoData)
		}
	}
	systray.SetTooltip("OneBoard Safe Paste — Right-click for menu")
	// Add menu items before starting the event loop so the menu is fully built when user clicks.
	mShow := systray.AddMenuItem("Show OneBoard Safe Paste", "Show window")
	mQuit := systray.AddMenuItem("Quit", "Quit OneBoard Safe Paste")
	systray.AddSeparator()
	trayMu.Lock()
	trayGhostStatusItem = systray.AddMenuItem("Ghost Mode: INACTIVE", "Ghost Mode status")
	trayMu.Unlock()
	trayGhostStatusItem.Disable()
	UpdateTrayMenu()
	// Single goroutine handling both click channels so neither blocks the other.
	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				if appForTray != nil && appForTray.ctx != nil {
					wailsruntime.EventsEmit(appForTray.ctx, TrayRequestShowEvent)
				}
			case <-mQuit.ClickedCh:
				if appForTray != nil && appForTray.ctx != nil {
					quittingFromTrayMu.Lock()
					quittingFromTray = true
					quittingFromTrayMu.Unlock()
					wailsruntime.EventsEmit(appForTray.ctx, TrayRequestQuitEvent)
				}
				return
			}
		}
	}()
}

func trayOnExit() {}

// LoadRules loads rules from settings.json. If the file does not exist or is empty,
// it gets default rules, saves them, and returns them so the frontend always receives a valid array.
// LoadRules does not use ghostMu; the clipboard watcher only locks ghostMu for ghostModeEnabled/lastClipboardText, so no deadlock.
func (a *App) LoadRules() ([]RedactionRule, error) {
	// Brief delay so the Wails bridge is ready before returning data (avoids 0 rules on first load).
	time.Sleep(100 * time.Millisecond)

	settingsPath, err := getSettingsPath()
	if err != nil {
		def := defaultRules()
		_ = a.SaveRules(def)
		debugLog("Backend loading rules: found %d rules (from getSettingsPath fallback)\n", len(def))
		debugLogln("Backend: Sending rules to frontend now...")
		return def, nil
	}

	b, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			def := defaultRules()
			_ = a.SaveRules(def)
			debugLog("Backend loading rules: found %d rules (file missing, wrote defaults)\n", len(def))
			debugLogln("Backend: Sending rules to frontend now...")
			return def, nil
		}
		def := defaultRules()
		debugLog("Backend loading rules: found %d rules (read error, returning defaults only)\n", len(def))
		debugLogln("Backend: Sending rules to frontend now...")
		return def, nil
	}

	var rules []RedactionRule
	if err := json.Unmarshal(b, &rules); err != nil {
		def := defaultRules()
		debugLog("Backend loading rules: found 0 rules (unmarshal error), returning %d defaults\n", len(def))
		debugLogln("Backend: Sending rules to frontend now...")
		return def, nil
	}
	if rules == nil {
		rules = []RedactionRule{}
	}
	if len(rules) == 0 {
		def := defaultRules()
		_ = a.SaveRules(def)
		debugLog("Backend loading rules: found 0 rules, re-ran defaults and returning %d rules\n", len(def))
		debugLogln("Backend: Sending rules to frontend now...")
		return def, nil
	}

	// Upgrade/migrate known built-in rules without overwriting user customization.
	if upgraded, updated := upgradeRules(rules); upgraded {
		rules = updated
		_ = a.SaveRules(rules)
	}
	// Deduplicate by Name: no two rules may have the exact same Name; keep the newer (last) one, delete older duplicates.
	prevLen := len(rules)
	rules = dedupeRulesByName(rules)
	if len(rules) < prevLen {
		_ = a.SaveRules(rules)
	}
	debugLog("Backend loading rules: found %d rules\n", len(rules))
	debugLogln("Backend: Sending rules to frontend now...")
	return rules, nil
}

// ResetRules deletes settings.json and re-initializes default Standard and Advanced rules.
// Returns the fresh array of rules for the frontend.
func (a *App) ResetRules() ([]RedactionRule, error) {
	settingsPath, err := getSettingsPath()
	if err != nil {
		return nil, err
	}
	_ = os.Remove(settingsPath)
	def := defaultRules()
	a.Rules = def
	if err := a.SaveRules(def); err != nil {
		return nil, err
	}
	return def, nil
}

// GetDefaultRules returns the built-in default rules so the frontend can show them when the file is missing or empty.
func (a *App) GetDefaultRules() ([]RedactionRule, error) {
	return defaultRules(), nil
}

func (a *App) SaveRules(rules []RedactionRule) error {
	// Never persist an empty list — avoid overwriting the file if frontend sends [] by mistake.
	if len(rules) == 0 {
		return nil
	}
	settingsPath, err := getSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		return err
	}

	// Normalize & backfill required fields so the file stays consistent.
	for i := range rules {
		if strings.TrimSpace(rules[i].Category) == "" {
			rules[i].Category = "System"
		}
		if strings.TrimSpace(rules[i].Source) == "" {
			rules[i].Source = inferSource(rules[i])
		}
	}

	b, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp := settingsPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}

	// Best-effort atomic replace across platforms.
	if err := os.Rename(tmp, settingsPath); err != nil {
		_ = os.Remove(settingsPath)
		if err2 := os.Rename(tmp, settingsPath); err2 != nil {
			_ = os.Remove(tmp)
			return err
		}
	}

	a.Rules = rules
	return nil
}

func (a *App) AddCustomRule(pattern string, isLiteral bool, replacement string, severity string, category string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}

	replacement = strings.TrimSpace(replacement)
	severity = strings.TrimSpace(severity)
	if severity == "" {
		severity = "Medium"
	}
	category = strings.TrimSpace(category)
	if category == "" {
		category = "System"
	}

	rules := a.Rules
	if len(rules) == 0 {
		if loaded, err := a.LoadRules(); err == nil {
			rules = loaded
		} else {
			rules = defaultRules()
		}
	}

	id := "custom-" + time.Now().UTC().Format("20060102T150405Z") + "-" + randSuffix(6)
	rule := RedactionRule{
		ID:          id,
		Name:        pattern,
		Pattern:     pattern,
		IsEnabled:   true,
		IsLiteral:   isLiteral,
		Category:    category,
		Source:      "Custom",
		Severity:    severity,
		Description: "User-defined rule",
		Replacement: replacement,
	}
	if rule.Replacement == "" {
		rule.Replacement = "[CUSTOM]"
	}

	rules = append(rules, rule)
	return a.SaveRules(rules)
}

const maxBulkItems = 300

// AddBulkCustomRule parses a newline/comma-separated list and adds one custom rule that matches
// any of the items (literal, word-boundary). maskType: "host"→[HOST], "user"→[USER], "ip"→[IP], else [REDACTED].
func (a *App) AddBulkCustomRule(listText string, maskType string, category string, severity string) error {
	items := parseBulkList(listText)
	if len(items) == 0 {
		return nil
	}
	if len(items) > maxBulkItems {
		items = items[:maxBulkItems]
	}

	repl := "[REDACTED]"
	nameSuffix := "items"
	switch strings.ToLower(strings.TrimSpace(maskType)) {
	case "host":
		repl = "[HOST]"
		nameSuffix = "Hostnames"
	case "user":
		repl = "[USER]"
		nameSuffix = "Usernames"
	case "ip":
		repl = "[IP]"
		nameSuffix = "IPs"
	}

	parts := make([]string, 0, len(items))
	for _, s := range items {
		parts = append(parts, regexp.QuoteMeta(s))
	}
	pattern := `\b(` + strings.Join(parts, "|") + `)\b`

	cat := strings.TrimSpace(category)
	if cat == "" {
		cat = "System"
	}
	sev := strings.TrimSpace(severity)
	if sev == "" {
		sev = "Medium"
	}
	name := fmt.Sprintf("Bulk: %s (%d items)", nameSuffix, len(items))
	return a.AddCustomRuleWithName(name, pattern, false, repl, sev, cat)
}

// AddCustomRuleWithName is like AddCustomRule but allows setting the display name (used by bulk).
func (a *App) AddCustomRuleWithName(name string, pattern string, isLiteral bool, replacement string, severity string, category string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}
	replacement = strings.TrimSpace(replacement)
	if replacement == "" {
		replacement = "[CUSTOM]"
	}
	severity = strings.TrimSpace(severity)
	if severity == "" {
		severity = "Medium"
	}
	category = strings.TrimSpace(category)
	if category == "" {
		category = "System"
	}
	rules := a.Rules
	if len(rules) == 0 {
		if loaded, err := a.LoadRules(); err == nil {
			rules = loaded
		} else {
			rules = defaultRules()
		}
	}
	id := "custom-" + time.Now().UTC().Format("20060102T150405Z") + "-" + randSuffix(6)
	rule := RedactionRule{
		ID:          id,
		Name:        name,
		Pattern:     pattern,
		IsEnabled:   true,
		IsLiteral:   isLiteral,
		Category:    category,
		Source:      "Custom",
		Severity:    severity,
		Description: "Bulk custom rule",
		Replacement: replacement,
	}
	rules = append(rules, rule)
	return a.SaveRules(rules)
}

func parseBulkList(listText string) []string {
	lines := strings.Split(listText, "\n")
	var out []string
	seen := make(map[string]bool)
	for _, line := range lines {
		for _, s := range strings.Split(line, ",") {
			s = strings.TrimSpace(s)
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (a *App) DeleteCustomRule(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}

	rules := a.Rules
	if len(rules) == 0 {
		if loaded, err := a.LoadRules(); err == nil {
			rules = loaded
		} else {
			rules = defaultRules()
		}
	}

	updated := make([]RedactionRule, 0, len(rules))
	removed := false
	for _, r := range rules {
		if r.ID == id {
			if strings.TrimSpace(r.Source) != "Custom" {
				// Only custom rules are deletable.
				updated = append(updated, r)
				continue
			}
			removed = true
			continue
		}
		updated = append(updated, r)
	}

	if !removed {
		return nil
	}
	return a.SaveRules(updated)
}

func inferSource(r RedactionRule) string {
	id := strings.TrimSpace(r.ID)
	if strings.HasPrefix(id, "custom-") {
		return "Custom"
	}
	// Back-compat: older versions stored custom rules as Category == "Custom".
	if strings.EqualFold(strings.TrimSpace(r.Category), "custom") {
		return "Custom"
	}
	return "Built-In"
}

// sanitizeRedactInput keeps only standard printable ASCII plus newline and tab so invisible/control
// characters cannot break JWT regex or hide "eyJ" probes.
func sanitizeRedactInput(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 32 && r <= 126) || r == '\n' || r == '\t' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// applyManualJWTFallback finds eyJ… spans delimited by space/semicolon (or line end) with at least one '.' (header.payload or 3-part) and replaces them with repl.
// Used when regexp matching fails (invisible bytes, engine quirks) to avoid missing obvious JWT-shaped tokens.
func applyManualJWTFallback(s, repl string) string {
	if repl == "" {
		repl = ruleReplJWT
	}
	var b strings.Builder
	b.Grow(len(s))
	pos := 0
	for pos < len(s) {
		rel := strings.Index(s[pos:], "eyJ")
		if rel < 0 {
			b.WriteString(s[pos:])
			break
		}
		i := pos + rel
		b.WriteString(s[pos:i])
		end := len(s)
		for j := i + 3; j < len(s); j++ {
			c := s[j]
			if c == ' ' || c == ';' || c == '\n' || c == '\t' {
				end = j
				break
			}
		}
		seg := s[i:end]
		if strings.Count(seg, ".") >= 1 && len(seg) >= 9 {
			b.WriteString(repl)
			pos = end
			debugLog("DEBUG: manual JWT fallback replaced segment (len=%d)\n", len(seg))
			continue
		}
		b.WriteString("eyJ")
		pos = i + 3
	}
	out := b.String()
	if strings.Contains(out, "eyJ") {
		out = applyManualJWTSplitJoinFallback(out, repl)
	}
	return out
}

// applyManualJWTSplitJoinFallback redacts JWT-shaped tokens when regex is still unreliable after sanitization:
// whitespace-delimited words that contain "eyJ", have a '.' after "eyJ", and at least one dot in the tail (unsigned or signed JWT),
// are replaced with repl (any prefix before "eyJ" in the same word is preserved).
func applyManualJWTSplitJoinFallback(s, repl string) string {
	if repl == "" {
		repl = ruleReplJWT
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return s
	}
	changed := false
	for i := range parts {
		p := parts[i]
		idx := strings.Index(p, "eyJ")
		if idx < 0 {
			continue
		}
		if !strings.Contains(p[idx+3:], ".") {
			continue
		}
		tail := p[idx:]
		if strings.Count(tail, ".") < 1 {
			continue
		}
		parts[i] = p[:idx] + repl
		changed = true
	}
	if !changed {
		return s
	}
	return strings.Join(parts, " ")
}

func (a *App) RedactText(input string, includeEventDetails bool) RedactResult {
	if input == "" || strings.TrimSpace(input) == "" {
		return RedactResult{RedactedText: input, Events: nil}
	}
	// Defensive copy: Wails passes an immutable Go string; copying makes redaction independent of any
	// concurrent []byte→string construction and keeps this buffer stable for the whole function.
	input = string(bytes.Clone([]byte(input)))
	cleanInput := sanitizeRedactInput(input)
	preview := cleanInput
	if len(preview) > 120 {
		preview = preview[:120] + "..."
	}
	debugLog("[Redact] Starting redaction on input (len=%d): %q\n", len(cleanInput), preview)

	if strings.Contains(cleanInput, "eyJ") {
		if jwtCoreRegexp.MatchString(cleanInput) {
			debugLogln("DEBUG: JWT Regex found a match!")
		} else {
			debugLogln("DEBUG: JWT Regex failed to see the token.")
			debugLogln("DEBUG: clean input still contains \"eyJ\" but probe did not match — check lookalike Unicode or nonstandard separators.")
			idx := strings.Index(cleanInput, "eyJ")
			if idx >= 0 {
				end := idx + 50
				if end > len(cleanInput) {
					end = len(cleanInput)
				}
				debugLog("HEX DUMP near eyJ (cleanInput): %x\n", cleanInput[idx:end])
			}
			if input != cleanInput {
				idx2 := strings.Index(input, "eyJ")
				if idx2 >= 0 {
					e2 := idx2 + 50
					if e2 > len(input) {
						e2 = len(input)
					}
					debugLog("HEX DUMP near eyJ (raw input): %x\n", input[idx2:e2])
				}
			}
		}
	}

	rules, err := a.LoadRules()
	if err != nil || len(rules) == 0 {
		rules = a.Rules
		if len(rules) == 0 {
			rules = defaultRules()
		}
	}
	a.Rules = rules

	ordered := orderRules(rules)
	debugLog("[Redact] Applying %d rules (enabled, non-empty pattern)\n", len(ordered))

	var events []RedactionEvent
	// Single working buffer for the returned RedactedText — rules only assign out from the previous out
	// (or ipv6 helper); none reset to cleanInput or raw input.
	out := cleanInput
	// Replacement for post-pipeline JWT cleanup; updated when std-jwt runs (defaults to ruleReplJWT).
	postJWTReplacement := ruleReplJWT

	for _, rule := range ordered {
		if !rule.IsEnabled {
			continue
		}
		// std-jwt uses built-in rulePatternJWT even if settings left Pattern empty.
		if rule.ID != "std-jwt" && strings.TrimSpace(rule.Pattern) == "" {
			continue
		}
		pat := rule.Pattern
		if rule.ID == "std-jwt" {
			pat = rulePatternJWT
		}
		if rule.IsLiteral {
			pat = regexp.QuoteMeta(pat)
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			debugLog("[Redact] Skip rule %q: regex compile error: %v\n", rule.Name, err)
			continue
		}
		repl := rule.Replacement
		if repl == "" {
			repl = "[REDACTED]"
		}

		if rule.ID == "std-ipv6" {
			var ipv6Events []RedactionEvent
			out, ipv6Events = replaceIPv6SkipSHA256WithEvents(out, re, repl, rule.Name)
			events = append(events, ipv6Events...)
		} else if rule.ID == "std-jwt" {
			storedJWT := strings.TrimSpace(rule.Pattern)
			switch {
			case storedJWT == "":
				debugLogln("[Redact] std-jwt integrity: persisted Pattern is EMPTY — using canonical rulePatternJWT only")
			default:
				if storedJWT != rulePatternJWT {
					if _, cerr := regexp.Compile(storedJWT); cerr != nil {
						debugLog("[Redact] std-jwt integrity: persisted Pattern does not compile (%v) — using canonical\n", cerr)
					} else {
						debugLogln("[Redact] std-jwt integrity: persisted Pattern differs from canonical (custom or stale); redaction still uses canonical matcher")
					}
				}
			}
			baseRepl := strings.TrimSpace(repl)
			if baseRepl == "" {
				baseRepl = ruleReplJWT
			}
			postJWTReplacement = baseRepl
			if jwtCoreRegexp.MatchString(out) {
				debugLogln("DEBUG: JWT Regex found a match!")
			} else {
				debugLogln("DEBUG: JWT Regex failed to see the token.")
			}
			matches := jwtCoreRegexp.FindAllStringIndex(out, -1)
			if len(matches) == 0 {
				if strings.Contains(out, "eyJ") {
					out = jwtFallbackReplaceRegexp.ReplaceAllString(out, baseRepl)
					if strings.Contains(out, "eyJ") {
						out = applyManualJWTSplitJoinFallback(out, baseRepl)
					}
					snap := out
					if len(snap) > 256 {
						snap = snap[:256] + "...(truncated)"
					}
					debugLog("DEBUG: String immediately after manual fix: %s\n", snap)
					debugLogln("DEBUG: Manual Fallback Redaction applied to out.")
				}
				continue
			}
			var b strings.Builder
			last := 0
			for _, pair := range matches {
				start, end := pair[0], pair[1]
				if start < last {
					continue
				}
				matched := out[start:end]
				dbg := matched
				if len(dbg) > jwtLogMatchMaxLen {
					dbg = dbg[:jwtLogMatchMaxLen] + "..."
				}
				debugLog("Checking JWT match against: %s\n", dbg)
				replOut := baseRepl
				b.WriteString(out[last:start])
				b.WriteString(replOut)
				events = append(events, RedactionEvent{
					RuleName:     rule.Name,
					OriginalText: matched,
					Replacement:  replOut,
				})
				last = end
			}
			b.WriteString(out[last:])
			out = b.String()
		} else {
			matches := re.FindAllStringSubmatchIndex(out, -1)
			if len(matches) == 0 {
				continue
			}
			var b strings.Builder
			last := 0
			for _, m := range matches {
				start, end := m[0], m[1]
				if start < last {
					continue
				}
				b.WriteString(out[last:start])
				b.WriteString(repl)
				events = append(events, RedactionEvent{
					RuleName:     rule.Name,
					OriginalText: out[start:end],
					Replacement:  repl,
				})
				last = end
			}
			b.WriteString(out[last:])
			out = b.String()
		}
		debugLog("[Redact] Rule applied: %q\n", rule.Name)
	}

	if strings.Contains(out, "eyJ") {
		prev := out
		out = applyManualJWTFallback(out, postJWTReplacement)
		if out != prev {
			debugLogln("DEBUG: manual JWT fallback modified output after rule pipeline")
		}
	}

	// Fill StartIndex/EndIndex in final redacted text (events are in order of appearance).
	pos := 0
	for i := range events {
		idx := strings.Index(out[pos:], events[i].Replacement)
		if idx < 0 {
			break
		}
		events[i].StartIndex = pos + idx
		events[i].EndIndex = events[i].StartIndex + len(events[i].Replacement)
		pos = events[i].EndIndex
	}

	outPreview := out
	if len(outPreview) > 120 {
		outPreview = outPreview[:120] + "..."
	}
	debugLog("[Redact] Finished redaction (output len=%d): %q\n", len(out), outPreview)
	time.Sleep(10 * time.Millisecond)
	if !includeEventDetails {
		return RedactResult{RedactedText: out, Events: nil}
	}
	return RedactResult{RedactedText: out, Events: events}
}

const (
	satelliteWidth  = 350
	satelliteHeight = 800
	normalWindowW   = 1024
	normalWindowH   = 768
)

// ToggleSatelliteMode switches between a slim always-on-top sidebar (350x800) and normal window.
// When active: saves current size, sets 350x800 and always on top. When inactive: restores saved or 1024x768.
func (a *App) ToggleSatelliteMode(active bool) {
	if a.ctx == nil {
		return
	}
	if active {
		w, h := wailsruntime.WindowGetSize(a.ctx)
		if w > 0 && h > 0 {
			a.savedWindowW, a.savedWindowH = w, h
		} else {
			a.savedWindowW, a.savedWindowH = normalWindowW, normalWindowH
		}
		wailsruntime.WindowSetSize(a.ctx, satelliteWidth, satelliteHeight)
		wailsruntime.WindowSetAlwaysOnTop(a.ctx, true)
	} else {
		w, h := a.savedWindowW, a.savedWindowH
		if w <= 0 || h <= 0 {
			w, h = normalWindowW, normalWindowH
		}
		wailsruntime.WindowSetSize(a.ctx, w, h)
		wailsruntime.WindowSetAlwaysOnTop(a.ctx, false)
	}
}

// SimulatePasteAfterDelay starts a goroutine that waits delayMs then simulates Paste (Ctrl+V / Cmd+V).
// Returns immediately so the frontend does not block. Use with Auto Paste + Auto-Copy.
func (a *App) SimulatePasteAfterDelay(delayMs int) {
	if delayMs <= 0 {
		delayMs = 500
	}
	go func() {
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
		kb, err := keybd_event.NewKeyBonding()
		if err != nil {
			debugLog("[SimulatePasteAfterDelay] keybd NewKeyBonding: %v\n", err)
			return
		}
		kb.SetKeys(keybd_event.VK_V)
		if runtime.GOOS == "darwin" {
			kb.HasSuper(true) // Cmd+V on Mac
		} else {
			kb.HasCTRL(true) // Ctrl+V on Windows/Linux
		}
		if err = kb.Launching(); err != nil {
			debugLog("[SimulatePasteAfterDelay] Launching: %v\n", err)
		}
	}()
}

// universalSecretShouldRedact is deprecated; Universal Secret Catch is now a pure quoted-string catch.


// replaceIPv6SkipSHA256 replaces IPv6 matches with repl, but skips any match immediately preceded by "SHA256:" (SSH fingerprint).
func replaceIPv6SkipSHA256(s string, re *regexp.Regexp, repl string) string {
	out, _ := replaceIPv6SkipSHA256WithEvents(s, re, repl, "")
	return out
}

// replaceIPv6SkipSHA256WithEvents does the same but returns events for debug mode.
func replaceIPv6SkipSHA256WithEvents(s string, re *regexp.Regexp, repl string, ruleName string) (string, []RedactionEvent) {
	const sha256Prefix = "SHA256:"
	idx := re.FindAllStringIndex(s, -1)
	if len(idx) == 0 {
		return s, nil
	}
	var events []RedactionEvent
	var b strings.Builder
	last := 0
	for _, pair := range idx {
		start, end := pair[0], pair[1]
		if start < last {
			continue
		}
		pre := ""
		if start >= len(sha256Prefix) {
			pre = s[start-len(sha256Prefix) : start]
		}
		b.WriteString(s[last:start])
		if pre == sha256Prefix {
			b.WriteString(s[start:end])
		} else {
			b.WriteString(repl)
			events = append(events, RedactionEvent{
				RuleName:     ruleName,
				OriginalText: s[start:end],
				Replacement:  repl,
			})
		}
		last = end
	}
	b.WriteString(s[last:])
	return b.String(), events
}

// bestGuessIsHighEntropy returns true if s contains at least one digit and (at least one uppercase letter or one symbol from @#$%^&*!).
func bestGuessIsHighEntropy(s string) bool {
	hasDigit := false
	hasUpperOrSymbol := false
	const symbols = "@#$%^&*!"
	for _, r := range s {
		if r >= '0' && r <= '9' {
			hasDigit = true
		}
		if r >= 'A' && r <= 'Z' {
			hasUpperOrSymbol = true
		}
		if strings.ContainsRune(symbols, r) {
			hasUpperOrSymbol = true
		}
		if hasDigit && hasUpperOrSymbol {
			return true
		}
	}
	return hasDigit && hasUpperOrSymbol
}

func getSettingsPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "SiloRedact", "settings.json"), nil
}

func defaultRules() []RedactionRule {
	return []RedactionRule{
		{
			ID:          "std-aws-access-key-id",
			Name:        "AWS Access Key ID",
			Pattern:     `\bAKIA[0-9A-Z]{10,20}\b`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "Critical",
			Description: "Detects AWS access key IDs (AKIA...).",
			Replacement: "[AWS_ID]",
		},
		{
			ID:          "std-aws-access-key-id-assign",
			Name:        "AWS Access Key ID (Assignment)",
			Pattern:     `(?i)(access_key_id|aws_access_key_id)\s*[:=]\s*['"]?([A-Z0-9]{20})['"]?`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "Critical",
			Description: "Detects AWS access key ID assignments. RE2-compatible, no backrefs.",
			Replacement: `$1: [SECRET]`,
		},
		{
			ID:          "std-aws-secret-access-key",
			Name:        "AWS Secret Access Key",
			Pattern:     `(?i)(aws_secret_access_key|secret|password|token|api_key|code)(?:\s*[:=]\s*['"]?)([A-Za-z0-9\/+]{20,60})(['"]?)`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "Critical",
			Description: "Key name followed by 20-60 base64-style characters.",
			Replacement: `$1=[SECRET]`,
		},
		{
			ID:          "std-jwt",
			Name:        "JWT",
			Pattern:     rulePatternJWT,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "Critical",
			Description: "Detects JSON Web Tokens ((?s) dot-all; three segments with literal dots; header/payload/signature allow + / = and URL-safe charset).",
			Replacement: ruleReplJWT,
		},
		{
			ID:          "std-uuid",
			Name:        "UUID / Session ID (RFC 4122)",
			Pattern:     rulePatternUUID,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "System",
			Severity:    "High",
			Description: "Standard 8-4-4-4-12 UUIDs for session IDs, request IDs, and correlation IDs in DevOps logs.",
			Replacement: ruleReplUUID,
		},
		{
			ID:          "std-generic-secret",
			Name:        "Secrets (Generic)",
			Pattern:     `(?i)(password|passwd|secret|token|api_key|auth|key|sk-live|credential)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^'"\s]{4,})`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "Critical",
			Description: "Detects common secret key=value; value double-quoted, single-quoted, or unquoted. RE2-compatible.",
			Replacement: `$1$2[SECRET]`,
		},
		{
			ID:          "std-private-key",
			Name:        "Private Key",
			Pattern:     `(?s)-----BEGIN [A-Z ]+ PRIVATE KEY-----.*?-----END [A-Z ]+ PRIVATE KEY-----`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "Critical",
			Description: "Detects PEM-encoded private keys (RSA, EC, etc.).",
			Replacement: "[KEY]",
		},
		{
			ID:          "adv-uri-credentials",
			Name:        "URI Credentials",
			Pattern:     `(?i)([a-z0-9]+:\/\/.*?:)(.*)(@.*)`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "High",
			Description: "Detects password in URIs; replaces only the password segment (greedy).",
			Replacement: "$1[SECRET]$3",
		},
		{
			ID:          "adv-ipv4",
			Name:        "IPv4 Address",
			Pattern:     `\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)\b`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Network",
			Severity:    "High",
			Description: "Detects IPv4 addresses.",
			Replacement: "[IPv4]",
		},
		{
			ID:          "std-ipv6",
			Name:        "IPv6 Address",
			Pattern:     `(?i)\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b|\b(?:[0-9a-f]{1,4}:){0,5}::(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4}\b`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Network",
			Severity:    "High",
			Description: "Detects IPv6 addresses (3+ colons or ::); skips SHA256: fingerprints. MAC runs first.",
			Replacement: "[IPv6]",
		},
		{
			ID:          "adv-credit-card",
			Name:        "Credit Card",
			Pattern:     `\b(?:\d[ -]*?){13,16}\b`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Sensitive Data",
			Severity:    "High",
			Description: "Detects 13-16 digit sequences with optional dashes or spaces (credit card style).",
			Replacement: "[CARD]",
		},
		{
			ID:          "adv-email",
			Name:        "Email Address",
			Pattern:     `\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Sensitive Data",
			Severity:    "High",
			Description: "Detects email addresses.",
			Replacement: "[EMAIL]",
		},
		{
			ID:          "adv-file-path",
			Name:        "File Path",
			Pattern:     `(?i)(?:[a-z]:\\(?:[^'"])+|/(?:[\w.-]+/)+[\w.-]+)`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "System",
			Severity:    "Medium",
			Description: "Windows and Unix paths; handles spaces, stops at closing quote or EOL. Runs first.",
			Replacement: "[PATH]",
		},
		{
			ID:          "adv-mac",
			Name:        "MAC Address",
			Pattern:     `\b(?:[0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}\b`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Network",
			Severity:    "Medium",
			Description: "Detects MAC addresses.",
			Replacement: "[MAC]",
		},
		{
			ID:          "adv-best-guess-password",
			Name:        "Universal Secret Catch",
			Pattern:     `(['"])[^\s'"]{8,64}['"]`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "High",
			Description: "Quoted secret catch (8-64 chars, no spaces). Path rule runs first and claims paths.",
			Replacement: "[SECRET]",
		},
	}
}

// dedupeRulesByName ensures no two rules have the exact same Name; keeps the newer (last) occurrence, removes older duplicates.
func dedupeRulesByName(rules []RedactionRule) []RedactionRule {
	byName := make(map[string]int, len(rules))
	out := make([]RedactionRule, 0, len(rules))
	for _, r := range rules {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = r.ID
		}
		if idx, ok := byName[name]; ok {
			out[idx] = r
		} else {
			byName[name] = len(out)
			out = append(out, r)
		}
	}
	return out
}

// severityByID maps known rule IDs to their severity for backfill.
var severityByID = map[string]string{
	"std-aws-access-key-id": "Critical", "std-aws-access-key-id-assign": "Critical",
	"std-aws-secret-access-key": "Critical", "std-jwt": "Critical", "std-uuid": "High", "std-generic-secret": "Critical",
	"std-private-key": "Critical",
	"std-email": "High", "std-ipv4": "High", "std-ipv6": "High", "adv-best-guess-password": "High",
	"adv-credit-card": "High", "adv-uri-credentials": "High",
	"adv-file-path": "Medium", "adv-mac": "Medium",
}

func upgradeRules(rules []RedactionRule) (bool, []RedactionRule) {
	changed := false

	byID := make(map[string]int, len(rules))
	for i := range rules {
		if rules[i].ID != "" {
			byID[rules[i].ID] = i
		}
	}

	// Backfill Severity for existing rules when empty.
	for i := range rules {
		if strings.TrimSpace(rules[i].Source) == "" {
			rules[i].Source = inferSource(rules[i])
			changed = true
		}
		if rules[i].Severity != "" {
			continue
		}
		if sev, ok := severityByID[rules[i].ID]; ok {
			rules[i].Severity = sev
			changed = true
		} else {
			rules[i].Severity = "Medium"
			changed = true
		}
	}

	// Upgrade: AWS Access Key ID regex (more flexible length).
	if idx, ok := byID["std-aws-access-key-id"]; ok {
		want := `\bAKIA[0-9A-Z]{10,20}\b`
		if rules[idx].Pattern != want {
			rules[idx].Pattern = want
			changed = true
		}
	}

	// Add: AWS access key assignment rule if missing.
	awsAssignPat := `(?i)(access_key_id|aws_access_key_id)\s*[:=]\s*['"]?([A-Z0-9]{20})['"]?`
	awsAssignRepl := `$1: [SECRET]`
	if _, ok := byID["std-aws-access-key-id-assign"]; !ok {
		rules = append(rules, RedactionRule{
			ID:          "std-aws-access-key-id-assign",
			Name:        "AWS Access Key ID (Assignment)",
			Pattern:     awsAssignPat,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "Critical",
			Description: "Detects AWS access key ID assignments. RE2-compatible, no backrefs.",
			Replacement: awsAssignRepl,
		})
		changed = true
	} else if idx, ok := byID["std-aws-access-key-id-assign"]; ok && (rules[idx].Pattern != awsAssignPat || rules[idx].Replacement != awsAssignRepl) {
		rules[idx].Pattern = awsAssignPat
		rules[idx].Replacement = awsAssignRepl
		changed = true
	}

	// Upgrade: AWS secret access key — key name + 20-60 base64 chars.
	if idx, ok := byID["std-aws-secret-access-key"]; ok {
		wantPat := `(?i)(aws_secret_access_key|secret|password|token|api_key|code)(?:\s*[:=]\s*['"]?)([A-Za-z0-9\/+]{20,60})(['"]?)`
		wantRepl := `$1=[SECRET]`
		if rules[idx].Pattern != wantPat {
			rules[idx].Pattern = wantPat
			changed = true
		}
		if rules[idx].Replacement != wantRepl {
			rules[idx].Replacement = wantRepl
			changed = true
		}
	}

	// Upgrade: JWT — optional Bearer prefix; signature segment allows +, /, and =.
	if idx, ok := byID["std-jwt"]; ok {
		wantDesc := "Detects JSON Web Tokens ((?s) dot-all; three segments with literal dots; header/payload/signature allow + / = and URL-safe charset)."
		if rules[idx].Pattern != rulePatternJWT {
			rules[idx].Pattern = rulePatternJWT
			changed = true
		}
		if rules[idx].Replacement != ruleReplJWT {
			rules[idx].Replacement = ruleReplJWT
			changed = true
		}
		if rules[idx].Description != wantDesc {
			rules[idx].Description = wantDesc
			changed = true
		}
	}

	// Add: UUID / session ID (RFC 4122) if missing.
	uuidDesc := "Standard 8-4-4-4-12 UUIDs for session IDs, request IDs, and correlation IDs in DevOps logs."
	if _, ok := byID["std-uuid"]; !ok {
		rules = append(rules, RedactionRule{
			ID:          "std-uuid",
			Name:        "UUID / Session ID (RFC 4122)",
			Pattern:     rulePatternUUID,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "System",
			Severity:    "High",
			Description: uuidDesc,
			Replacement: ruleReplUUID,
		})
		changed = true
	} else if idx, ok := byID["std-uuid"]; ok {
		if rules[idx].Pattern != rulePatternUUID {
			rules[idx].Pattern = rulePatternUUID
			changed = true
		}
		if rules[idx].Description != uuidDesc {
			rules[idx].Description = uuidDesc
			changed = true
		}
		if rules[idx].Replacement != ruleReplUUID {
			rules[idx].Replacement = ruleReplUUID
			changed = true
		}
		if rules[idx].Category != "System" {
			rules[idx].Category = "System"
			changed = true
		}
	}

	// Upgrade: Generic secret rule — RE2-compatible: double-quoted, single-quoted, or unquoted value.
	if idx, ok := byID["std-generic-secret"]; ok {
		wantPat := `(?i)(password|passwd|secret|token|api_key|auth|key|sk-live|credential)(\s*[:=]\s*)("[^"]*"|'[^']*'|[^'"\s]{4,})`
		wantRepl := `$1$2[SECRET]`
		if rules[idx].Pattern != wantPat {
			rules[idx].Pattern = wantPat
			changed = true
		}
		if rules[idx].Replacement != wantRepl {
			rules[idx].Replacement = wantRepl
			changed = true
		}
	}

	// Add: Private Key rule if missing.
	if _, ok := byID["std-private-key"]; !ok {
		rules = append(rules, RedactionRule{
			ID:          "std-private-key",
			Name:        "Private Key",
			Pattern:     `(?s)-----BEGIN [A-Z ]+ PRIVATE KEY-----.*?-----END [A-Z ]+ PRIVATE KEY-----`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "Critical",
			Description: "Detects PEM-encoded private keys (RSA, EC, etc.).",
			Replacement: "[KEY]",
		})
		changed = true
	}

	uriCredRule := RedactionRule{
		ID:          "adv-uri-credentials",
		Name:        "URI Credentials",
		Pattern:     `(?i)([a-z0-9]+:\/\/.*?:)(.*)(@.*)`,
		IsEnabled:   true,
		IsLiteral:   false,
		Category:    "Cloud & Auth",
		Severity:    "High",
		Description: "Detects password in URIs; replaces only the password segment.",
		Replacement: "$1[SECRET]$3",
	}
	if _, ok := byID["adv-uri-credentials"]; !ok {
		if idx, ok := byID["adv-ipv4"]; ok {
			rules = append(rules[:idx], append([]RedactionRule{uriCredRule}, rules[idx:]...)...)
		} else {
			rules = append(rules, uriCredRule)
		}
		changed = true
	} else if idx, ok := byID["adv-uri-credentials"]; ok {
		wantPat := `(?i)([a-z0-9]+:\/\/.*?:)(.*)(@.*)`
		wantRepl := "$1[SECRET]$3"
		if rules[idx].Pattern != wantPat {
			rules[idx].Pattern = wantPat
			changed = true
		}
		if rules[idx].Replacement != wantRepl {
			rules[idx].Replacement = wantRepl
			changed = true
		}
		if rules[idx].Category != "Cloud & Auth" {
			rules[idx].Category = "Cloud & Auth"
			changed = true
		}
	}

	// Add: IPv6 Address rule if missing.
	ipv6Pat := `(?i)\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b|\b(?:[0-9a-f]{1,4}:){0,5}::(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4}\b`
	ipv6Desc := "Detects IPv6 addresses (3+ colons or ::); skips SHA256: fingerprints. MAC runs first."
	if _, ok := byID["std-ipv6"]; !ok {
		rules = append(rules, RedactionRule{
			ID:          "std-ipv6",
			Name:        "IPv6 Address",
			Pattern:     ipv6Pat,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Network",
			Severity:    "High",
			Description: ipv6Desc,
			Replacement: "[IPv6]",
		})
		changed = true
	} else if idx, ok := byID["std-ipv6"]; ok {
		if rules[idx].Pattern != ipv6Pat {
			rules[idx].Pattern = ipv6Pat
			rules[idx].Description = ipv6Desc
			changed = true
		}
		if rules[idx].Category != "Network" {
			rules[idx].Category = "Network"
			changed = true
		}
		if rules[idx].Replacement != "[IPv6]" {
			rules[idx].Replacement = "[IPv6]"
			changed = true
		}
	}

	// Add: Credit Card rule (Advanced) if missing.
	ccPat := `\b(?:\d[ -]*?){13,16}\b`
	ccDesc := "Detects 13-16 digit sequences with optional dashes or spaces (credit card style)."
	if _, ok := byID["adv-credit-card"]; !ok {
		rules = append(rules, RedactionRule{
			ID:          "adv-credit-card",
			Name:        "Credit Card",
			Pattern:     ccPat,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Sensitive Data",
			Severity:    "High",
			Description: ccDesc,
			Replacement: "[CARD]",
		})
		changed = true
	} else if idx, ok := byID["adv-credit-card"]; ok {
		if rules[idx].Pattern != ccPat {
			rules[idx].Pattern = ccPat
			rules[idx].Description = ccDesc
			changed = true
		}
		if rules[idx].Category != "Sensitive Data" {
			rules[idx].Category = "Sensitive Data"
			changed = true
		}
		if rules[idx].Replacement != "[CARD]" {
			rules[idx].Replacement = "[CARD]"
			changed = true
		}
	}

	// Add: Universal Secret Catch rule if missing — Path runs first and claims paths; this catches remaining quoted secrets.
	if _, ok := byID["adv-best-guess-password"]; !ok {
		rules = append(rules, RedactionRule{
			ID:          "adv-best-guess-password",
			Name:        "Universal Secret Catch",
			Pattern:     `(['"])[^\s'"]{8,64}['"]`,
			IsEnabled:   true,
			IsLiteral:   false,
			Category:    "Cloud & Auth",
			Severity:    "High",
			Description: "Quoted secret catch (8-64 chars, no spaces). Path rule runs first and claims paths.",
			Replacement: "[SECRET]",
		})
		changed = true
	} else {
		idx := byID["adv-best-guess-password"]
		wantName := "Universal Secret Catch"
		wantPat := `(['"])[^\s'"]{8,64}['"]`
		wantRepl := "[SECRET]"
		wantDesc := "Quoted secret catch (8-64 chars, no spaces). Path rule runs first and claims paths."
		if rules[idx].Name != wantName {
			rules[idx].Name = wantName
			changed = true
		}
		if rules[idx].Pattern != wantPat {
			rules[idx].Pattern = wantPat
			changed = true
		}
		if rules[idx].Replacement != wantRepl {
			rules[idx].Replacement = wantRepl
			changed = true
		}
		if rules[idx].Description != wantDesc {
			rules[idx].Description = wantDesc
			changed = true
		}
		if rules[idx].Category != "Cloud & Auth" {
			rules[idx].Category = "Cloud & Auth"
			changed = true
		}
	}

	// Upgrade: File path — Windows and Unix, spaces allowed, stops at quote or EOL.
	if idx, ok := byID["adv-file-path"]; ok {
		wantPat := `(?i)(?:[a-z]:\\(?:[^'"])+|/(?:[\w.-]+/)+[\w.-]+)`
		wantRepl := "[PATH]"
		if rules[idx].Pattern != wantPat {
			rules[idx].Pattern = wantPat
			changed = true
		}
		if rules[idx].Replacement != wantRepl {
			rules[idx].Replacement = wantRepl
			changed = true
		}
		if rules[idx].Category != "System" {
			rules[idx].Category = "System"
			changed = true
		}
	}
	// Migrate remaining built-in rules to new categories and replacements (by ID).
	catRepl := map[string]struct{ cat, repl string }{
		"std-aws-access-key-id":             {"Cloud & Auth", "[AWS_ID]"},
		"std-aws-access-key-id-assign":     {"Cloud & Auth", ""}, // already upgraded above
		"std-aws-secret-access-key":        {"Cloud & Auth", ""},
		"std-jwt":                          {"Cloud & Auth", ruleReplJWT},
		"std-uuid":                         {"System", ruleReplUUID},
		"std-generic-secret":                {"Cloud & Auth", ""},
		"std-private-key":                  {"Cloud & Auth", ""},
		"adv-uri-credentials":              {"Cloud & Auth", ""},
		"adv-ipv4":                         {"Network", "[IPv4]"},
		"adv-mac":                          {"Network", "[MAC]"},
		"adv-email":                        {"Sensitive Data", "[EMAIL]"},
	}
	for id, v := range catRepl {
		if v.repl == "" {
			continue
		}
		if idx, ok := byID[id]; ok {
			if rules[idx].Category != v.cat {
				rules[idx].Category = v.cat
				changed = true
			}
			if rules[idx].Replacement != v.repl {
				rules[idx].Replacement = v.repl
				changed = true
			}
		}
	}

	return changed, rules
}

// redactOrder: std-jwt / std-uuid first so URI or generic secrets never nibble host/session text before JWT runs.
var redactOrder = []string{
	"std-jwt", "std-uuid",
	"adv-file-path",
	"adv-uri-credentials",
	"std-private-key",
	"std-aws-access-key-id", "std-aws-access-key-id-assign", "std-aws-secret-access-key",
	"std-generic-secret",
	"adv-mac", "adv-ipv4", "std-ipv6",
	"adv-best-guess-password",
}

func orderRules(rules []RedactionRule) []RedactionRule {
	byID := make(map[string]RedactionRule, len(rules))
	for _, r := range rules {
		if r.ID != "" {
			byID[r.ID] = r
		}
	}
	out := make([]RedactionRule, 0, len(rules))
	seen := make(map[string]bool, len(rules))

	// Apply exact order for known rule IDs.
	for _, id := range redactOrder {
		if r, ok := byID[id]; ok {
			out = append(out, r)
			seen[id] = true
		}
	}
	// All other rules after.
	for _, r := range rules {
		if r.ID != "" && !seen[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

func randSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Deterministic fallback is fine for IDs; not used for security.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
