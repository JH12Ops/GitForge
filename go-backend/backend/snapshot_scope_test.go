package backend

import (
	"context"
	"testing"
)

// snapshotScopeRepo creates a temp repo with a commit on a side branch that
// the main branch does not contain, so head/all scopes differ.
func snapshotScopeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, c := range [][]string{
		{"init", "-q", "-b", "main", dir},
		{"-C", dir, "config", "user.email", "test@example.com"},
		{"-C", dir, "config", "user.name", "Test"},
		{"-C", dir, "commit", "--allow-empty", "-m", "base on main"},
		{"-C", dir, "checkout", "-q", "-b", "side"},
		{"-C", dir, "commit", "--allow-empty", "-m", "side branch only"},
		{"-C", dir, "checkout", "-q", "main"},
	} {
		_, err := RunGit(context.Background(), "", c...)

		if err != nil {
			t.Fatalf("setup git %v failed: %v", c, err)
		}
	}

	return dir
}

func snapshotSubjects(t *testing.T, dir string, opts SnapshotOptions) []string {
	t.Helper()
	snap, gerr := OpenRepo(context.Background(), dir, opts)

	if gerr != nil {
		t.Fatalf("OpenRepo: %v", gerr)
	}

	if snap.Snapshot == nil {
		t.Fatal("OpenRepo returned no snapshot")
	}

	subs := make([]string, 0, len(snap.Snapshot.Commits))

	for _, c := range snap.Snapshot.Commits {
		subs = append(subs, c.Subject)
	}

	return subs
}

func TestOpenRepoHonorsGraphScope(t *testing.T) {
	dir := snapshotScopeRepo(t)

	// Default (no scope) keeps the historical --all behavior.
	subs := snapshotSubjects(t, dir, SnapshotOptions{})

	if !contains(subs, "side branch only") {
		t.Fatalf("default scope must include side branch, got %v", subs)
	}

	// head scope shows only the checked-out branch's history.
	subs = snapshotSubjects(t, dir, SnapshotOptions{GraphScope: "head"})

	if contains(subs, "side branch only") {
		t.Fatalf("head scope leaked side-branch commit: %v", subs)
	}

	if !contains(subs, "base on main") {
		t.Fatalf("head scope lost the branch commit: %v", subs)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}

	return false
}
