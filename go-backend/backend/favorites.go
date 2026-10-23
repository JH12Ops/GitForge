package backend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

/* Favorites: per-repository pinned branches.

The renderer pins branches in the sidebar; the pins persist in app support
(keyed by canonical repo path), and snapshots mark matching branch rows
isFavorite=true so the state survives every backend swap. Starter:
Genre favorite file layout: {"<repoPath>": ["main", "release/*"]} */

type FavoritesStore struct {
	mu    sync.Mutex
	path  string
	cache map[string][]string
}

var favoritesStore = newFavoritesStore()

func newFavoritesStore() *FavoritesStore {
	store := &FavoritesStore{}

	if configDir, err := os.UserConfigDir(); err == nil {
		store.path = filepath.Join(configDir, "gitforge", "favorites.json")
	}

	return store
}

// UserConfigDir is XDG_CONFIG_HOME/mac Application Support; fall back to
// home if unset.
func userFavoritesDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "gitforge")
	}

	home, _ := os.UserHomeDir()

	return filepath.Join(home, ".gitforge")
}

func (f *FavoritesStore) load() map[string][]string {
	if f.cache != nil {
		return f.cache
	}

	f.cache = map[string][]string{}

	if f.path == "" {
		f.path = filepath.Join(userFavoritesDir(), "favorites.json")
	}

	data, err := os.ReadFile(f.path)

	if err == nil {
		_ = json.Unmarshal(data, &f.cache)
	}

	return f.cache
}

func (f *FavoritesStore) get(repoPath string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.load()[repoPath]
}

func (f *FavoritesStore) set(repoPath string, names []string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := names

	if out == nil {
		out = []string{} // arrays, never null
	}

	if f.cache == nil {
		f.load()
	}

	if len(out) == 0 {
		delete(f.cache, repoPath)
	} else {
		f.cache[repoPath] = out
	}

	if len(f.cache) > 0 {
		// Stable file for diffing/backups.
		for repo := range f.cache {
			sort.Strings(f.cache[repo])
		}
	}

	if f.path == "" {
		f.path = filepath.Join(userFavoritesDir(), "favorites.json")
	}

	_ = os.MkdirAll(filepath.Dir(f.path), 0o755)
	data, err := json.MarshalIndent(f.cache, "", "  ")

	if err == nil {
		_ = osWriteFileAtomic(f.path, data)
	}

	return out
}

func osWriteFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"

	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

func favoritesFor(repoPath string) map[string]bool {
	out := map[string]bool{}

	for _, name := range favoritesStore.get(repoPath) {
		out[name] = true
	}

	return out
}

var _ = context.Background
