package main

import (
	"os"
	"syscall"

	"gitforge/go-backend/backend"

	"github.com/wailsapp/wails/v3/pkg/application"
)

/* Native application menu (wails shell parity with the Tauri app's
src/lib/tauri/appMenu.ts). Item labels mirror appMenuLabels; behaviour lives
where it belongs:

- Window/zoom/fullscreen/quit/hide → native here (instant, no IPC hop).
- Renderer-state actions (open/clone repository, settings, tab switching…)
  → SSE `menu:action` events handled by src/lib/wails/menuBridge.ts.
- The Window → Tabs submenu mirrors the renderer's tab list via git_set_tabs. */

const (
	appZoomMin  = 0.5
	appZoomMax  = 2
	appZoomStep = 0.1
)

// Menu labels mirror src/lib/tauri/appMenu.ts (keep in sync there).
const (
	menuCheckUpdates   = "Check for Updates…"
	menuSettings       = "Settings…"
	menuQuit           = "Quit GitForge"
	menuFile           = "File"
	menuNewTab         = "New Tab"
	menuCloseTab       = "Close Tab"
	menuReopenTab      = "Reopen Closed Tab"
	menuOpenRepository = "Open Repository…"
	menuCloneRepo      = "Clone Repository…"
	menuInitRepo       = "Initialize Repository…"
	menuView           = "View"
	menuRelaunch       = "Relaunch GitForge"
	menuFullscreen     = "Toggle Full Screen"
	menuZoomIn         = "Increase Zoom"
	menuZoomOut        = "Decrease Zoom"
	menuZoomReset      = "Reset Zoom"
	menuWindow         = "Window"
	menuMinimize       = "Minimize"
	menuZoomWindow     = "Zoom"
	menuTabs           = "Tabs"
	menuNoOpenTabs     = "No Open Tabs"
	menuHelp           = "Help"
)

type menuState struct {
	zoom    float64
	tabsSub *application.Menu
}

var menu = &menuState{zoom: 1}

func clampZoom(next float64) float64 {
	if next > appZoomMax {
		next = appZoomMax
	}

	if next < appZoomMin {
		next = appZoomMin
	}

	// Round off float drift, as the renderer's clampZoom does.
	return float64(int64(next*100+0.5)) / 100
}

func zoomWindow(delta int) {
	menu.zoom = clampZoom(menu.zoom + float64(delta)*appZoomStep)

	if win := application.Get().Window.Current(); win != nil {
		win.SetZoom(menu.zoom)
	}
}

func zoomWindowReset() {
	menu.zoom = 1

	if win := application.Get().Window.Current(); win != nil {
		win.SetZoom(1)
	}
}

func currentWindow() application.Window {
	return application.Get().Window.Current()
}

// relaunch re-execs this exact binary; the watcher/SSE die with the process
// and the renderer restores its session from localStorage on boot.
func relaunch() {
	if exe, err := os.Executable(); err == nil {
		_ = syscall.Exec(exe, os.Args, os.Environ())
	}
}

// menuAction wraps a renderer-dispatched action into a menu item callback.
func menuAction(action, url, path string) func(*application.Context) {
	return func(*application.Context) {
		backend.PublishMenuAction(action, backend.MenuActionPayload{URL: url, Path: path})
	}
}

// appLinks mirror HELP_ITEMS in appMenu.ts (which resolves URLs from
// @shared/app/metadata at build time; GitForge uses the same fork targets).
var appLinks = []struct{ label, url string }{
	// TODO: point at the real GitForge repo once origin exists (no remote yet).
	{"GitForge Website", "https://github.com/gitforge/gitforge"},
	{"GitHub Repository", "https://github.com/gitforge/gitforge"},
	{"Report an Issue", "https://github.com/gitforge/gitforge/issues"},
	{"Releases & Downloads", "https://github.com/gitforge/gitforge/releases"},
}

func buildAppMenu() *application.Menu {
	bar := application.NewMenu()

	// AppKit uses the FIRST bar item's submenu as the application menu (its
	// title is replaced with the app name); every top-level entry must be a
	// submenu, so About/Hide/Quit live in here — not on the bar itself.
	// Mirrors Wails' own NewAppMenu() shape.
	app := bar.AddSubmenu("GitForge")
	app.AddRole(application.About)
	app.AddSeparator()
	app.Add(menuCheckUpdates).OnClick(menuAction("check-updates", "", ""))
	app.Add(menuSettings).SetAccelerator("cmdorctrl+,").OnClick(menuAction("open-settings", "", ""))
	app.AddSeparator()

	for _, link := range appLinks {
		app.Add(link.label).OnClick(menuAction("open-external", link.url, ""))
	}

	app.AddSeparator()
	app.AddRole(application.Hide)
	app.AddRole(application.HideOthers)
	app.AddRole(application.UnHide)
	app.AddSeparator()
	app.AddRole(application.Quit)

	file := bar.AddSubmenu(menuFile)
	file.Add(menuNewTab).SetAccelerator("cmdorctrl+t").OnClick(menuAction("new-tab", "", ""))
	file.Add(menuCloseTab).SetAccelerator("cmdorctrl+w").OnClick(menuAction("close-tab", "", ""))
	file.Add(menuReopenTab).SetAccelerator("cmdorctrl+shift+t").OnClick(menuAction("reopen-closed-tab", "", ""))
	file.AddSeparator()
	file.Add(menuCloneRepo).OnClick(menuAction("clone-repository", "", ""))
	file.Add(menuInitRepo).OnClick(menuAction("init-repository", "", ""))
	file.Add(menuOpenRepository).OnClick(menuAction("open-repository", "", ""))

	view := bar.AddSubmenu(menuView)
	view.Add(menuRelaunch).OnClick(func(*application.Context) { relaunch() })
	view.Add(menuFullscreen).SetAccelerator("ctrl+cmd+f").OnClick(func(*application.Context) {
		if win := currentWindow(); win != nil {
			win.ToggleFullscreen()
		}
	})
	view.AddSeparator()
	view.Add(menuZoomIn).SetAccelerator("cmdorctrl+shift+=").OnClick(func(*application.Context) { zoomWindow(1) })
	view.Add(menuZoomOut).SetAccelerator("cmdorctrl+shift+-").OnClick(func(*application.Context) { zoomWindow(-1) })
	view.Add(menuZoomReset).SetAccelerator("cmdorctrl+shift+0").OnClick(func(*application.Context) { zoomWindowReset() })

	windowMenu := bar.AddSubmenu(menuWindow)
	windowMenu.Add(menuMinimize).SetAccelerator("cmdorctrl+m").OnClick(func(*application.Context) {
		if win := currentWindow(); win != nil {
			win.Minimise()
		}
	})
	windowMenu.Add(menuZoomWindow).OnClick(func(*application.Context) {
		if win := currentWindow(); win != nil {
			win.ToggleMaximise()
		}
	})
	windowMenu.AddSeparator()
	menu.tabsSub = windowMenu.AddSubmenu(menuTabs)
	rebuildTabsMenu()

	help := bar.AddSubmenu(menuHelp)

	for _, link := range appLinks {
		help.Add(link.label).OnClick(menuAction("open-external", link.url, ""))
	}

	return bar
}

// rebuildTabsMenu regenerates Window → Tabs from the renderer's tab list
// (runs on the main thread via the tabs-changed hook).
func rebuildTabsMenu() {
	sub := menu.tabsSub

	if sub == nil {
		return
	}

	state := backend.GetAppTabs()
	sub.Clear()

	if len(state.Tabs) == 0 {
		empty := sub.Add(menuNoOpenTabs)
		empty.SetEnabled(false)
	} else {
		for _, tab := range state.Tabs {
			sub.Add(tab.Name).
				SetChecked(tab.Path == state.ActivePath).
				OnClick(menuAction("switch-tab", "", tab.Path))
		}
	}

	sub.Update()
}

// installAppMenu wires the native menu; call once before app.Run().
func installAppMenu() {
	application.Get().Menu.SetApplicationMenu(buildAppMenu())
	// The renderer pushes its tab list over the gateway; rebuild on change.
	backend.OnAppTabsChanged = func() {
		application.InvokeSync(rebuildTabsMenu)
	}
}
