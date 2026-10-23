// Dev gateway — headless GitForge gateway for browser development.
//
// `npm run dev` (scripts/dev.mjs) builds and runs this command, watching
// .go files for changes, and proxies the renderer's /gitforge/* traffic to it
// (GITFORGE_LOCAL_TEST_ADDR, default 127.0.0.1:18333). The app therefore runs
// with real Go-backed git data while vite keeps frontend HMR — no desktop
// window, no Wails app loop. Note: native dialog routes (folder pickers) are
// unavailable without the desktop shell; open repositories through the
// session restore or favorites instead.
package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"gitforge/go-backend/backend"
)

func main() {
	addr := os.Getenv("GITFORGE_LOCAL_TEST_ADDR")

	if addr == "" {
		addr = "127.0.0.1:18333"
	}

	// Same persistent diagnostic sink as the desktop shell.
	if home, err := os.UserHomeDir(); err == nil {
		backend.SetLogSink(filepath.Join(home, "Library", "Logs", "gitforge.log"))
	}

	// dist is optional here: statics are unused in browser dev (vite serves
	// the app), only the API/SSE routes matter.
	distDir, _ := backend.ResolveDistDir()

	ln, err := net.Listen("tcp", addr)

	if err != nil {
		log.Fatalf("devgateway: %v", err)
	}

	log.Printf("[devgateway] ready on http://%s", addr)

	if err := http.Serve(ln, backend.NewGateway(distDir)); err != nil {
		log.Fatalf("devgateway: %v", err)
	}
}
