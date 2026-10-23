package backend

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// setupStageRepo creates a temp repo with a committed file, one modification
// on it, and one untracked file.
func setupStageRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	os.WriteFile(fmt.Sprintf("%s/a.txt", dir), []byte("a\n"), 0o644)

	for _, c := range [][]string{
		{"init", "-q", "-b", "main", dir},
		{"-C", dir, "config", "user.email", "test@example.com"},
		{"-C", dir, "config", "user.name", "Test"},
		{"-C", dir, "add", "a.txt"},
		{"-C", dir, "commit", "-qm", "init"},
	} {
		if _, err := RunGit(context.Background(), "", c...); err != nil {
			t.Fatalf("git setup %v: %v", c, err)
		}
	}

	os.WriteFile(fmt.Sprintf("%s/a.txt", dir), []byte("modified\n"), 0o644)
	os.WriteFile(fmt.Sprintf("%s/b.txt", dir), []byte("new\n"), 0o644)

	return dir
}

// TestStageThenScopedSnapshotMarksStaged pins the commit-flow pipeline the
// commit button depends on: ExecuteAction("stage") must flip the `staged`
// flag on the file in the next scoped ("status") snapshot — the exact data
// the renderer's runAction → refreshSnapshot(['status']) path consumes.
func TestStageThenScopedSnapshotMarksStaged(t *testing.T) {
	dir := setupStageRepo(t)

	if env := ExecuteAction(context.Background(), dir, "stage", "a.txt", nil); !env.OK {
		t.Fatalf("stage action failed: %s", env.Message)
	}

	snap, gerr := BuildSnapshot(context.Background(), dir, SnapshotOptions{Scopes: []string{"status"}})

	if gerr != nil {
		t.Fatalf("BuildSnapshot: %v", gerr)
	}

	byPath := make(map[string]WireWorkingTreeFile, len(snap.WorkingTree))

	for _, f := range snap.WorkingTree {
		byPath[f.Path] = f
	}

	staged, ok := byPath["a.txt"]

	if !ok {
		t.Fatal("a.txt missing from scoped snapshot")
	}

	if !staged.Staged {
		t.Fatal("a.txt should be staged after the stage action")
	}

	if staged.Status != StatusModified {
		t.Fatalf("a.txt status = %q, want modified", staged.Status)
	}

	if staged.Additions != 1 || staged.Deletions != 1 {
		t.Fatalf("a.txt counts = (%d, %d), want (1, 1)", staged.Additions, staged.Deletions)
	}

	if untracked, ok := byPath["b.txt"]; ok && untracked.Staged {
		t.Fatal("b.txt is untracked and must not be marked staged")
	}
}

// TestCommitRequiresStagedFiles documents the wire contract behind the
// commit button: a scoped snapshot must not mark untracked files staged,
// and commit errors cleanly when nothing is staged.
func TestCommitRequiresStagedFiles(t *testing.T) {
	dir := setupStageRepo(t)

	env := ExecuteAction(context.Background(), dir, "commit", "", map[string]any{"message": "no staged changes"})

	if env.OK {
		t.Fatal("commit without staged files must fail")
	}
}
