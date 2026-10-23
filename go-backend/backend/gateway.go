package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

/* API gateway.

In the Wails build the asset handler routes everything under
/gitforge/api/<command> (POST JSON in / JSON out) and /gitforge/events (SSE)
to this dispatcher; everything else is static files from dist/. Using plain
HTTP over the same-origin asset server means zero Wails-binding glue: the wire
contract is exactly shared/git/models.ts, the same JSON the Tauri backend
spoke. It also makes the whole backend testable with curl. */

// NewGateway returns the asset HTTP handler for the webview (statics + API).
func NewGateway(distDir string) http.Handler {
	statics := http.FileServer(http.Dir(distDir))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleAPI(w, r) {
			return
		}

		// SPA fallback: unknown non-file routes get index.html (the renderer
		// is a single-page Vue app). The served page is stamped with the
		// backend marker so renderer-side detection never depends on fetch
		// behavior inside this webview.
		name := filepath.Base(r.URL.Path)

		if r.URL.Path == "/" || filepath.Ext(name) == "" {
			if html, err := osReadFile(filepath.Join(distDir, "index.html")); err == nil {
				stamped := stampIndexHTML(html)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(stamped)

				return
			}
		}

		statics.ServeHTTP(w, r)
	})
}

const backendMarker = `<script>window.__GITFORGE_BACKEND__='wails'</script>`

// The Wails JS runtime (served by the wails asset middleware at /wails/*)
// drives window dragging (--wails-draggable CSS) in titlebar-less layout.
const runtimeMarker = `<script type="module" src="/wails/runtime.js"></script>`

// stampIndexHTML injects the backend marker before any module script so
// renderer code can detect Wails synchronously (fetch on the asset scheme
// must not be a prerequisite for service selection).
func stampIndexHTML(src []byte) []byte {
	if bytes.Contains(src, []byte(backendMarker)) {
		return src
	}

	inject := append([]byte(backendMarker), runtimeMarker...)
	lower := bytes.ToLower(src)

	if i := bytes.Index(lower, []byte("<head>")); i >= 0 {
		out := make([]byte, 0, len(src)+len(inject))
		out = append(out, src[:i+len("<head>")]...)
		out = append(out, inject...)
		out = append(out, src[i+len("<head>"):]...)

		return out
	}

	return src
}

// SSE hub. Producers arrive with the watcher port; the stream is kept warm
// until then.
type eventHub struct {
	mu      sync.Mutex
	streams map[chan []byte]bool
}

var hub = &eventHub{streams: map[chan []byte]bool{}}

func (h *eventHub) add() chan []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, 32)
	h.streams[ch] = true

	return ch
}

func (h *eventHub) remove(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.streams, ch)
}

// Publish sends an SSE event to every connected stream (non-blocking).
func (h *eventHub) Publish(event, payload string) {
	frame := fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload)
	h.mu.Lock()
	defer h.mu.Unlock()

	for ch := range h.streams {
		select {
		case ch <- []byte(frame):
		default: // slow consumer: drop
		}
	}
}

// PublishWindowFocus tells the renderer the native window regained key
// status, so it can re-check repository changes that landed while the app
// was inactive (worktree edits, fetches from other tools). Fired by the
// main package's WindowDidBecomeMain hook.
func PublishWindowFocus() {
	hub.Publish("git:focus", "{}")
}

func handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)

	if !ok {
		http.Error(w, "no streaming support", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := hub.add()
	defer hub.remove(ch)
	// Hello frame + keepalives so webview proxies never cut the stream.
	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()
	keepalive := newTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case frame := <-ch:
			if _, err := w.Write(frame); err != nil {
				return
			}

			flusher.Flush()

		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}

/* ------------------------------ API routing -------------------------------- */

// apiFunc = endpoint implementation. req is the decoded JSON body; the
// response is written as JSON (200 unless respondErr).
type apiFunc func(ctx context.Context, body []byte) (any, *GitError)

var apiRoutes = map[string]apiFunc{}

func route(name string, fn apiFunc) { apiRoutes[name] = fn }

func init() {
	route("ping", func(_ context.Context, body []byte) (any, *GitError) {
		var req struct {
			Value *string `json:"value"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Value == nil {
			return map[string]any{"value": nil}, nil
		}

		return map[string]any{"value": *req.Value}, nil
	})

	route("gitforge_log", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Msg string `json:"msg"`
			Src string `json:"src"`
		}
		_ = json.Unmarshal(body, &req)
		appendLog(fmt.Sprintf("[renderer/%s] %s\n", srcOr(req.Src), req.Msg))

		return map[string]any{"ok": true}, nil
	})

	route("git_version", func(ctx context.Context, _ []byte) (any, *GitError) {
		return map[string]string{"version": cachedGitVersion(ctx)}, nil
	})

	route("git_open_repo", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string          `json:"path"`
			Options SnapshotOptions `json:"options"`
		}
		_ = json.Unmarshal(body, &req)

		return openOrErr(ctx, req.Path, req.Options)
	})

	route("git_pick_and_open_repo", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Options SnapshotOptions `json:"options"`
		}
		_ = json.Unmarshal(body, &req)

		return pickAndOpen(ctx, req.Options), nil
	})

	route("git_activate_repo", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(body, &req)
		abs, gerr := ValidateRepo(ctx, req.Path)

		if gerr != nil {
			return OpenRepoResult{OK: false, Error: gerr.Serialize()}, nil
		}

		WatchRepo(abs)

		return map[string]any{"ok": true, "path": abs}, nil
	})

	route("git_snapshot", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string          `json:"path"`
			Options SnapshotOptions `json:"options"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Path == "" {
			return nil, gitErr(ErrCodeUnknown, "snapshot: no repository path", nil)
		}

		return BuildSnapshot(ctx, req.Path, req.Options)
	})

	route("git_commit_page", func(ctx context.Context, body []byte) (any, *GitError) {
		var req CommitPageRequestWire
		_ = json.Unmarshal(body, &req)

		return CommitPage(ctx, req)
	})

	route("git_get_diff", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string          `json:"path"`
			Request DiffRequestWire `json:"request"`
		}
		_ = json.Unmarshal(body, &req)

		return FetchDiff(ctx, req.Path, req.Request)
	})

	route("git_get_commit_files", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string `json:"path"`
			Request struct {
				Sha string `json:"sha"`
			} `json:"request"`
		}
		_ = json.Unmarshal(body, &req)

		return CommitFiles(ctx, req.Path, req.Request.Sha)
	})

	route("git_get_compare_files", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string `json:"path"`
			Request struct {
				FromSha string `json:"fromSha"`
				ToSha   string `json:"toSha"`
			} `json:"request"`
		}
		_ = json.Unmarshal(body, &req)

		return CompareFiles(ctx, req.Path, req.Request.FromSha, req.Request.ToSha)
	})

	route("git_search", func(ctx context.Context, body []byte) (any, *GitError) {
		var req SearchRequestWire
		_ = json.Unmarshal(body, &req)

		return Search(ctx, req)
	})

	route("git_clone_repo", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Request CloneRepoRequestWire `json:"request"`
			Options SnapshotOptions      `json:"options"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Request.Options.GraphScope == "" {
			req.Request.Options = req.Options
		}

		return CloneRepo(ctx, req.Request)
	})

	route("git_init_repo", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Request InitRepoRequestWire `json:"request"`
			Options SnapshotOptions     `json:"options"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Request.Options.GraphScope == "" {
			req.Request.Options = req.Options
		}

		return InitRepo(ctx, req.Request)
	})

	route("git_discover_repos", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Request DiscoverReposRequestWire `json:"request"`
		}
		_ = json.Unmarshal(body, &req)

		return DiscoverRepos(ctx, req.Request), nil
	})

	route("git_close_repo", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Path != "" {
			abs, err := filepath.Abs(req.Path)

			if err == nil {
				UnwatchRepo(abs)
				clearJournal(abs)
			} else {
				UnwatchRepo(req.Path)
				clearJournal(req.Path)
			}
		}

		return map[string]any{"ok": true}, nil
	})

	route("git_cancel", func(ctx context.Context, _ []byte) (any, *GitError) {
		cancelled := inFlight.cancelAll()

		return map[string]any{"ok": true, "cancelled": cancelled}, nil
	})

	route("git_execute_action", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path   string `json:"path"`
			Action struct {
				Kind   string         `json:"kind"`
				Target *string        `json:"target"`
				Meta   map[string]any `json:"meta"`
			} `json:"action"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Path == "" {
			return envelopeErr(unknownErr("No repository open.")), nil
		}

		abs, gerr := ValidateRepo(ctx, req.Path)

		if gerr != nil {
			return envelopeErr(gerr), nil
		}

		target := ""

		if req.Action.Target != nil {
			target = *req.Action.Target
		}

		meta := req.Action.Meta

		if meta == nil {
			meta = map[string]any{}
		}

		return ExecuteAction(ctx, abs, req.Action.Kind, target, meta), nil
	})

	route("git_open_terminal_external", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(body, &req)
		ok, msg := OpenTerminalExternal(req.Path)

		return map[string]any{"ok": ok, "message": msg}, nil
	})

	route("git_reveal_path", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(body, &req)
		ok, msg := RevealPath(req.Path)

		return map[string]any{"ok": ok, "message": msg}, nil
	})

	// Read-side niceties ported with the action router (image preview stays a
	// renderer-cache hit in most flows).
	route("git_get_blame", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string          `json:"path"`
			Request DiffRequestWire `json:"request"`
		}
		_ = json.Unmarshal(body, &req)

		return FetchBlame(ctx, req.Path, req.Request)
	})

	route("git_get_file_history", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string `json:"path"`
			Request struct {
				Path  string `json:"path"`
				Limit int    `json:"limit"`
			} `json:"request"`
		}
		_ = json.Unmarshal(body, &req)

		return FetchFileHistory(ctx, req.Path, req.Request.Path, req.Request.Limit)
	})

	route("git_get_favorites", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Request struct {
				RepoPath string `json:"repoPath"`
			} `json:"request"`
		}
		_ = json.Unmarshal(body, &req)
		favorites := favoritesStore.get(req.Request.RepoPath)

		if favorites == nil {
			favorites = []string{}
		}

		return favorites, nil
	})

	route("git_set_favorites", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Request struct {
				RepoPath string   `json:"repoPath"`
				Names    []string `json:"names"`
			} `json:"request"`
		}
		_ = json.Unmarshal(body, &req)
		names := favoritesStore.set(req.Request.RepoPath, req.Request.Names)

		return map[string]any{"names": names, "repoPath": req.Request.RepoPath}, nil
	})

	route("git_preload_repo", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string          `json:"path"`
			Options SnapshotOptions `json:"options"`
		}
		_ = json.Unmarshal(body, &req)
		// Warms a background tab; touches no per-renderer gateway state.
		abs, gerr := ValidateRepo(ctx, req.Path)

		if gerr != nil {
			return OpenRepoResult{OK: false, Error: gerr.Serialize()}, nil
		}

		snap, gerr := BuildSnapshot(ctx, abs, req.Options)

		if gerr != nil {
			return OpenRepoResult{OK: false, Error: gerr.Serialize()}, nil
		}

		return OpenRepoResult{OK: true, Snapshot: snap}, nil
	})

	route("git_get_image_preview", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string          `json:"path"`
			Request DiffRequestWire `json:"request"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Path == "" {
			return nil, unknownErr("No repository open.")
		}

		abs, gerr := ValidateRepo(ctx, req.Path)

		if gerr != nil {
			return nil, gerr
		}

		return FetchImagePreview(ctx, abs, req.Request)
	})

	route("git_get_conflict", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string `json:"path"`
			Request struct {
				Path string `json:"path"`
			} `json:"request"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Path == "" {
			return nil, unknownErr("No repository open.")
		}

		abs, gerr := ValidateRepo(ctx, req.Path)

		if gerr != nil {
			return nil, gerr
		}

		return FetchConflict(ctx, abs, req.Request.Path)
	})

	route("git_get_rebase_commits", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Path    string `json:"path"`
			Request struct {
				Base string `json:"base"`
			} `json:"request"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Path == "" {
			return nil, unknownErr("No repository open.")
		}

		abs, gerr := ValidateRepo(ctx, req.Path)

		if gerr != nil {
			return nil, gerr
		}

		return FetchRebaseCommits(ctx, abs, req.Request.Base)
	})

	// wails shell parity (menu bridge)
	route("git_set_tabs", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Tabs       []AppTab `json:"tabs"`
			ActivePath string   `json:"activePath"`
		}
		_ = json.Unmarshal(body, &req)
		SetAppTabs(req.Tabs, req.ActivePath)

		return map[string]any{"ok": true}, nil
	})

	route("git_open_external", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(body, &req)

		if gerr := openExternalSystem(req.URL); gerr != nil {
			return nil, gerr
		}

		return map[string]any{"ok": true}, nil
	})

	route("git_set_window_background", func(ctx context.Context, body []byte) (any, *GitError) {
		var req struct {
			Color string `json:"color"`
		}
		_ = json.Unmarshal(body, &req)

		if AcceptWindowBackground != nil {
			// The wails window lives in the main package; color mapping and
			// main-thread dispatch happen in the hook.
			AcceptWindowBackground(req.Color)
		}

		return map[string]any{"ok": true}, nil
	})

	route("git_pick_directory", func(ctx context.Context, body []byte) (any, *GitError) {
		path, gerr := pickDirectory()

		if gerr != nil {
			return nil, gerr
		}

		return map[string]any{"path": path}, nil
	})
}

func srcOr(src string) string {
	if strings.TrimSpace(src) == "" {
		return "no-source"
	}

	return src
}

func openOrErr(ctx context.Context, path string, opts SnapshotOptions) (any, *GitError) {
	result, gerr := OpenRepo(ctx, path, opts)

	if gerr != nil {
		return OpenRepoResult{OK: false, Error: gerr.Serialize()}, nil
	}

	WatchRepo(result.Snapshot.Repository.Path)

	return result, nil
}

func handleAPI(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path

	switch {
	case path == "/gitforge/events":
		access(r, 0, nil)
		handleSSE(w, r)

		return true

	case strings.HasPrefix(path, "/gitforge/api/"):
		start := time.Now()
		handleCommand(w, r, strings.TrimPrefix(path, "/gitforge/api/"))
		access(r, time.Since(start), nil)

		return true
	}

	return false
}

func access(r *http.Request, dur time.Duration, _ error) {
	if logSink == "" {
		return
	}

	line := fmt.Sprintf("%s %s dur=%s ua=%s\n", r.Method, r.URL.Path, dur, r.UserAgent())
	appendLog(line)
}

// appendLog mirrors gateway/rendered-client messages into a persistent file
// so release builds (no devtools) remain diagnosable.
func appendLog(line string) {
	_ = osWriteFile(logSink, []byte(time.Now().Format("15:04:05.000 ")+line), true)
}

func handleCommand(w http.ResponseWriter, r *http.Request, name string) {
	fn, ok := apiRoutes[name]

	if !ok {
		logNote("unknown command: " + name)
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"message": "unknown command: " + name})

		return
	}

	body := readBody(r)
	result, gerr := fn(r.Context(), body)

	if gerr != nil {
		logNote(fmt.Sprintf("%s -> error %s %s", name, gerr.Code, gerr.Message))
		writeJSONStatus(w, http.StatusBadGateway, gerr.Serialize())

		return
	}

	writeJSONStatus(w, http.StatusOK, result)
}

func writeJSONStatus(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

const maxBodyBytes = 4 << 20

func readBody(r *http.Request) []byte {
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 64*1024)

	for {
		n, err := r.Body.Read(tmp)

		if n > 0 && len(buf) <= maxBodyBytes {
			buf = append(buf, tmp[:n]...)
		}

		if err != nil {
			break
		}
	}

	return buf
}
