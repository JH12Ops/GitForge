// gitforge — Wails 3 (pure-Go) desktop shell.
//
// The webview asset server routes /gitforge/api/<command> and /gitforge/events
// (SSE) into the Go git layer (backend package); the remaining static files
// are served from ../dist. The renderer talks the exact JSON wire contract of
// shared/git/models.ts.
//
// Run:
//
//	go run .            (from this directory; expects ../dist to exist)
package main

import (
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"gitforge/go-backend/backend"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// appIconPNG is the 1024² true-transparency logo. Set as the runtime
// application icon so the macOS Dock always shows the GitForge mark even when
// LaunchServices' cached bundle icon is stale (e.g. after dev rebuilds), and
// for unbundled `go run .` runs.
//
//go:embed appicon.png
var appIconPNG []byte

// localBuildHash returns the short commit hash the binary was built from, or
// "" for release builds (BUILT by CI, where GITHUB_ACTIONS is set) and builds
// outside a git checkout (e.g. release tarballs), which keep the plain
// version everywhere. The go toolchain stamps vcs.revision automatically —
// no ldflags wiring needed.
func localBuildHash() string {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		return ""
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
			return setting.Value[:7]
		}
	}

	return ""
}

// aboutDescription builds the native About dialog message (Wails renders the
// app options' Name + Description as the About text). Local builds append the
// commit hash so the About panel matches the "Local Build: <hash>" shown by
// the renderer.
func aboutDescription() string {
	description := "A fast, beautiful Git client — GitForge"
	if hash := localBuildHash(); hash != "" {
		description += "\n\nLocal Build: " + hash
	}

	return description
}

func main() {
	distDir, err := resolveDistDir()

	if err != nil {
		log.Fatalf("gitforge: %v (build the frontend first: cd .. && npm run build)", err)
	}

	log.Printf("serving frontend from %s", distDir)

	// Persistent diagnostic sink: gateway access entries + renderer-relayed
	// errors land in ~/Library/Logs/gitforge.log even in release builds.
	if home, herr := os.UserHomeDir(); herr == nil {
		backend.SetLogSink(filepath.Join(home, "Library", "Logs", "gitforge.log"))
	}

	description := aboutDescription()

	// Optional twin HTTP listener for backend tests (curl):
	//   GITFORGE_LOCAL_TEST_ADDR=127.0.0.1:18333 go run .
	if testAddr := os.Getenv("GITFORGE_LOCAL_TEST_ADDR"); testAddr != "" {
		go func() {
			log.Printf("test gateway listening on http://%s", testAddr)
			log.Fatal(http.ListenAndServe(testAddr, backend.NewGateway(distDir)))
		}()
	}

	app := application.New(application.Options{
		Name:        "GitForge",
		Description: description,
		Icon:        appIconPNG,
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Assets: application.AssetOptions{
			Handler: backend.NewGateway(distDir),
		},
	})

	// Native folder picker for "Open repository…" — wired here because the
	// dialog manager belongs to the Wails app, not the backend package.
	backend.PickAndOpenFunc = func() (string, error) {
		result, err := app.Dialog.OpenFile().
			CanChooseDirectories(true).
			CanChooseFiles(false).
			CanCreateDirectories(false).
			ShowHiddenFiles(true).
			PromptForSingleSelection()

		if err != nil {
			return "", err
		}

		return strings.TrimSpace(result), nil
	}

	// Same panel for clone/init/scan ("pick a folder, don't open yet").
	backend.PickDirectoryFunc = func() (string, error) {
		result, err := app.Dialog.OpenFile().
			CanChooseDirectories(true).
			CanChooseFiles(false).
			CanCreateDirectories(false).
			ShowHiddenFiles(true).
			PromptForSingleSelection()

		if err != nil {
			return "", err
		}

		return strings.TrimSpace(result), nil
	}

	// Self-probe: three seconds after boot the webview exercises its own
	// gateway (fetch + marker + runtime globals). Results are relayed to the
	// log sink, which makes release-build webviews diagnosable without
	// devtools.
	selfProbe := `try {
  const log = (m) => fetch('/gitforge/api/gitforge_log', {method:'POST', body: JSON.stringify({src:'selfprobe', msg: m})}).catch(()=>{});
  log('page ' + location.href + ' marker=' + String(window.__GITFORGE_BACKEND__) + ' wailsGlobal=' + String(!!window.wails));
  fetch('/gitforge/api/ping', {method:'POST', headers:{'content-type':'application/json'}, body:'{"value":"probe"}'})
    .then(r => log('probe ' + r.status + ' ' + r.headers.get('content-type')))
    .catch(e => log('probe-fail ' + String(e)));
} catch (e) {
  try { fetch('/gitforge/api/gitforge_log', {method:'POST', body: JSON.stringify({src:'selfprobe', msg:'outer-catch ' + String(e)})}); } catch (e2) {}
}`
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "GitForge",
		Width:  1280,
		Height: 840,
		// Dark boot background (THEME_APP_COLORS.default): kills the white
		// flash; the renderer corrects it once the theme resolves.
		BackgroundColour: application.RGBA{Red: 0x12, Green: 0x15, Blue: 0x1a, Alpha: 255},
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInsetUnified,
		},
		URL: "/",
	})

	go func() {
		time.Sleep(3 * time.Second)
		window.ExecJS(selfProbe)
	}()

	// Pin the traffic lights to the RepoTabs row: AppKit centers them on the
	// unified titlebar row, which lands mid-title in our two-row top bar.
	// app.Run() is required before InvokeSync works, so the hooks below do
	// the initial application too (they fire moments after the window shows).
	window.OnWindowEvent(events.Mac.WebViewDidFinishNavigation, func(*application.WindowEvent) {
		alignTrafficLights(window)
	})
	window.OnWindowEvent(events.Mac.WindowDidExitFullScreen, func(*application.WindowEvent) {
		alignTrafficLights(window)
	})
	window.OnWindowEvent(events.Mac.WindowDidBecomeMain, func(*application.WindowEvent) {
		alignTrafficLights(window)
		// The window is active again: make the renderer re-check repository
		// changes (full snapshot + silent fetch) that landed while it slept.
		backend.PublishWindowFocus()
	})

	// Native application menu (wails shell parity with the Tauri appMenu).
	installAppMenu()

	// Native window background follows the renderer theme (applyTheme calls
	// setWindowBackground on every theme change).
	backend.AcceptWindowBackground = func(hexColor string) {
		if r, g, b, ok := parseHexRGB(hexColor); ok {
			application.InvokeSync(func() {
				window.SetBackgroundColour(application.RGBA{Red: r, Green: g, Blue: b, Alpha: 255})
			})
		}
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// parseHexRGB accepts #rrggbb (and #rgb) as used by THEME_APP_COLORS.
func parseHexRGB(hex string) (uint8, uint8, uint8, bool) {
	hex = strings.TrimPrefix(hex, "#")

	switch len(hex) {
	case 6:
		v, err := strconv.ParseUint(hex, 16, 32)

		if err != nil {
			return 0, 0, 0, false
		}

		return uint8(v >> 16), uint8(v >> 8), uint8(v), true

	case 3:
		var r, g, b uint8

		if _, err := fmt.Sscanf(hex, "%1x%1x%1x", &r, &g, &b); err != nil {
			return 0, 0, 0, false
		}

		return r * 17, g * 17, b * 17, true

	default:
		return 0, 0, 0, false
	}
}

// resolveDistDir is backend.ResolveDistDir (shared with cmd/devgateway).
func resolveDistDir() (string, error) {
	return backend.ResolveDistDir()
}
