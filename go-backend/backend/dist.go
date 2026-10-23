package backend

import (
	"os"
	"path/filepath"
)

// ResolveDistDir locates the built frontend (dist/index.html). From a bundle
// layout the exe is GitForge.app/Contents/MacOS/GitForge and the frontend sits
// four levels up at the repo root; from `go run .` it is two levels up. Walk
// upward from both the executable and the working directory until
// dist/index.html is found.
//
// Shared by the desktop shell (main) and the headless browser-dev gateway
// (cmd/devgateway). A missing dist is not fatal for the headless gateway: the
// API/SSE routes work without statics.
func ResolveDistDir() (string, error) {
	roots := []string{}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		roots = append(roots, exeDir)
		// inside a bundle: also look for the frontend in Contents/Resources/dist
		roots = append(roots, filepath.Join(filepath.Dir(exeDir), "Resources"))
	}

	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, wd)
	}

	for _, root := range roots {
		dir := root

		for i := 0; i < 8 && dir != filepath.Dir(dir); i++ {
			candidate := filepath.Join(dir, "dist")

			if st, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !st.IsDir() {
				return candidate, nil
			}

			dir = filepath.Dir(dir)
		}
	}

	return "", os.ErrNotExist
}
