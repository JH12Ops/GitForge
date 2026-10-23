package backend

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

/* Menu bridge (wails shell parity): the native menu lives in the Go main
package, but its items act on renderer state (repository tabs) and need to
dispatch actions back into the renderer. Channel-wise:

- renderer → Go: git_set_tabs (open repository tabs + active), so the native
  Window → Tabs submenu can mirror the tab bar.
- Go → renderer: `menu:action` SSE events for items whose behaviour lives in
  renderer stores (open/clone repository, settings, links, tab switching).
- window/zoom/fullscreen/relaunch are handled natively in Go.
*/

type AppTab struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type AppTabsState struct {
	Tabs       []AppTab `json:"tabs"`
	ActivePath string   `json:"activePath"`
}

var appTabsMu sync.Mutex
var appTabsState = AppTabsState{Tabs: []AppTab{}}

// OnAppTabsChanged is set by the main package so menu rebuilds can be
// dispatched on the main thread when the tab list changes.
var OnAppTabsChanged func()

// AcceptWindowBackground is set by the main package: renders the theme app
// color into the native window (hex like #12151a; alpha 255).
var AcceptWindowBackground func(hexColor string)

// PickDirectoryFunc is set by the main package: shows the native directory
// picker and returns the chosen path ("" on cancel).
var PickDirectoryFunc func() (string, error)

func pickDirectory() (string, *GitError) {
	if PickDirectoryFunc == nil {
		return "", nil // no native bridge (test twin listener): silent cancel
	}

	path, err := PickDirectoryFunc()

	if err != nil {
		// Cancel bubbles as an error from the dialog API; treat any failure
		// as "no selection" so the renderer never errors a user cancelling.
		return "", nil
	}

	return path, nil
}

func GetAppTabs() AppTabsState {
	appTabsMu.Lock()
	defer appTabsMu.Unlock()

	return appTabsState
}

// SetAppTabs stores the renderer's tab list for the native Tabs menu.
func SetAppTabs(tabs []AppTab, activePath string) {
	st := AppTabsState{Tabs: []AppTab{}, ActivePath: activePath}

	for _, t := range tabs {
		t.Path = strings.TrimSpace(t.Path)

		if t.Path == "" {
			continue
		}

		if t.Name == "" {
			t.Name = baseName(t.Path)
		}

		st.Tabs = append(st.Tabs, t)
	}

	appTabsMu.Lock()
	appTabsState = st
	appTabsMu.Unlock()

	if OnAppTabsChanged != nil {
		// Runs on whatever goroutine posted → the menu rebuild hops to the
		// main thread itself.
		OnAppTabsChanged()
	}
}

func baseName(path string) string {
	path = strings.TrimRight(path, "/\\")

	if idx := strings.LastIndexAny(path, "/\\"); idx >= 0 {
		return path[idx+1:]
	}

	return path
}

// MenuActionPayload travels to the renderer over SSE `menu:action`.
type MenuActionPayload struct {
	Action string `json:"action"`
	URL    string `json:"url,omitempty"`
	Path   string `json:"path,omitempty"`
}

// PublishMenuAction sends a native-menu action into the renderer.
func PublishMenuAction(action string, payload MenuActionPayload) {
	payload.Action = action
	hub.Publish("menu:action", mustJSON(payload))
}

// openExternalSystem runs the OS URL opener (mirrors tauri opener plugin).
func openExternalSystem(rawURL string) *GitError {
	trimmed := strings.TrimSpace(rawURL)
	// Only http(s) to keep the OS opener from serving drag-and-drop of odd
	// schemes; anchors/params stay intact.
	lower := strings.ToLower(trimmed)

	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return unknownErr("Only http(s) URLs can be opened externally.")
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", trimmed)

	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", trimmed)

	default:
		cmd = exec.Command("xdg-open", trimmed)
	}

	if err := cmd.Start(); err != nil {
		return unknownErr("could not open URL: " + err.Error())
	}

	go func() { _ = cmd.Wait() }() // reap
	return nil
}
