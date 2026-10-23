package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

/* Watcher port (watcher.rs): watch the .git directory (+ refs subtrees) of an
activated repository and publish SSE `git:changed` events with coarse scopes,
so the renderer can refresh only what likely moved.

V1 deliberately watches git plumbing only (index/HEAD/refs/in-progress
operations); worktree file edits surface via the renderer's periodic `status`
poll and window-focus checks, which the coalescer cannot mask. */

const watchCoalesceWindow = 250 * time.Millisecond

type watcherHub struct {
	mu       sync.Mutex
	watchers map[string]*repoWatcher // canonical repo path → watcher
}

var watchers = &watcherHub{watchers: map[string]*repoWatcher{}}

// WatchRepo starts watching a repository (idempotent per repo). Safe to call
// from any command scope.
func WatchRepo(repoPath string) {
	abs, err := filepath.Abs(repoPath)

	if err != nil || abs == "" {
		return
	}

	gitDir := gitDirOrNil(abs)

	if gitDir == "" {
		return
	}

	watchers.mu.Lock()
	defer watchers.mu.Unlock()

	if _, ok := watchers.watchers[abs]; ok {
		return
	}

	sysWatcher, werr := fsnotify.NewWatcher()

	if werr != nil {
		logNote("watcher: " + werr.Error())

		return
	}

	// Atomic rewrites replace inodes, so watch *directories*, not the files
	// themselves (index, HEAD, packed-refs, MERGE_HEAD… all live in gitDir).
	dirs := []string{
		gitDir,
		filepath.Join(gitDir, "refs"),
		filepath.Join(gitDir, "refs", "heads"),
		filepath.Join(gitDir, "refs", "remotes"),
		abs, // shallow worktree view (new/deleted top-level entries)
	}

	for _, dir := range dirs {
		if fi, serr := os.Stat(dir); serr == nil && fi.IsDir() {
			if aerr := sysWatcher.Add(dir); aerr != nil {
				logNote("watcher add " + dir + ": " + aerr.Error())
			}
		}
	}

	rw := &repoWatcher{
		path:   abs,
		system: sysWatcher,
		scopes: map[string]bool{},
		stop:   make(chan struct{}),
	}
	go rw.run()
	watchers.watchers[abs] = rw
	logNote(fmt.Sprintf("watcher: registered %s (%d dirs)", abs, len(dirs)))
}

// UnwatchRepo stops watching (on close).
func UnwatchRepo(repoPath string) {
	abs, err := filepath.Abs(repoPath)

	if err != nil {
		return
	}

	watchers.mu.Lock()
	defer watchers.mu.Unlock()

	if rw, ok := watchers.watchers[abs]; ok {
		close(rw.stop)
		_ = rw.system.Close()
		delete(watchers.watchers, abs)
	}
}

type repoWatcher struct {
	path   string
	system *fsnotify.Watcher
	scopes map[string]bool
	timer  *time.Timer
	stop   chan struct{}

	mu sync.Mutex
}

// scopesForPath maps a changed entry inside/below the git dir to snapshot
// scopes (mirror of watcher.rs's interest classes).
func scopesForPath(full string) []string {
	name := filepath.Base(full)
	gitRoot, rest, found := strings.Cut(full, ".git"+string(filepath.Separator))
	rel := ""

	if found {
		rel = rest
	} else {
		rel = name // top-level worktree entry
	}

	_ = gitRoot

	switch {
	case name == "index":
		return []string{"status", "commits"}

	case name == "COMMIT_EDITMSG" || name == "COMMIT_EDITMSG.lock":
		return []string{"status", "commits"}

	case strings.HasPrefix(rel, "refs"+string(filepath.Separator)) || name == "packed-refs":
		return []string{"branches", "commits"}

	case name == "HEAD" || name == "ORIG_HEAD" || name == "FETCH_HEAD" || name == "MERGE_HEAD":
		return []string{"branches", "status"}

	case name == "REBASE_HEAD" || name == "CHERRY_PICK_HEAD" || name == "REVERT_HEAD" ||
		name == "BISECT_LOG" || baseIsRebaseDir(rel):
		return []string{"status", "branches"}

	default:
		return nil
	}
}

// baseIsRebaseDir reports whether rel points inside rebase-merge/rebase-apply.
func baseIsRebaseDir(rel string) bool {
	return strings.HasPrefix(rel, "rebase-merge"+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "rebase-apply"+string(filepath.Separator)) ||
		rel == "rebase-merge" || rel == "rebase-apply"
}

func (rw *repoWatcher) run() {
	for {
		select {
		case event, ok := <-rw.system.Events:
			if !ok {
				return
			}

			// NOTE: chok... fsnotify's macOS kqueue backend replays stale
			// CHMOD events in a loop after a single touch (mtime never
			// changes), so CHMOD is deliberately ignored — it would create
			// an endless publish storm. Real git activity (index/refs
			// rewrites) arrives as Create/Rename/Write.
			if !event.Op.Has(fsnotify.Create) && !event.Op.Has(fsnotify.Write) &&
				!event.Op.Has(fsnotify.Remove) && !event.Op.Has(fsnotify.Rename) {
				continue
			}

			rw.accumulate(event.Name)

		case err, ok := <-rw.system.Errors:
			if !ok {
				return
			}

			logNote(fmtWatcherError(rw.path, err))

		case <-rw.stop:
			return
		}
	}
}

func fmtWatcherError(path string, err error) string {
	return "watcher " + path + ": " + err.Error()
}

func (rw *repoWatcher) accumulate(name string) {
	scopes := scopesForPath(name)

	if len(scopes) == 0 {
		return
	}

	logNote(fmt.Sprintf("watcher: event %s → %v", name, scopes))
	rw.mu.Lock()

	for _, s := range scopes {
		rw.scopes[s] = true
	}

	if rw.timer != nil {
		rw.timer.Stop()
	}

	rw.timer = time.AfterFunc(watchCoalesceWindow, rw.flush)
	rw.mu.Unlock()
}

// flush publishes one coalesced event and clears the aggregate.
func (rw *repoWatcher) flush() {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.timer != nil {
		rw.timer.Stop()
		rw.timer = nil
	}

	if len(rw.scopes) == 0 {
		return
	}

	scopes := make([]string, 0, len(rw.scopes))

	for s := range rw.scopes {
		scopes = append(scopes, s)
	}

	sort.Strings(scopes)
	rw.scopes = map[string]bool{}
	payload, err := json.Marshal(map[string]any{"path": rw.path, "scopes": scopes})

	if err != nil {
		return
	}

	hub.Publish("git:changed", string(payload))
	logNote(fmt.Sprintf("watcher: published %s %s", rw.path, scopes))
}
