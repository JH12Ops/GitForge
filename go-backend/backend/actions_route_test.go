package backend

import (
	"context"
	"strings"
	"testing"
)

// pullBranchContext assembles the routeAction dependencies for a temp repo.
func pullBranchContext(t *testing.T, dir string) actionContext {
	t.Helper()

	return actionContext{
		ctx:        context.Background(),
		repo:       dir,
		onProgress: func(string) {},
		repoState:  detectRepoState(dir, gitDirOrNil(dir), false),
		journal:    journalFor(dir),
		log:        &commandLog{},
	}
}

func headTip(t *testing.T, dir, ref string) string {
	t.Helper()
	res, err := RunGit(context.Background(), dir, "rev-parse", ref)

	if err != nil || !res.Ok() {
		t.Fatalf("rev-parse %s in %s failed: %v", ref, dir, err)
	}

	return strings.TrimSpace(res.Stdout)
}

// A local-branch upstream (branch.<x>.remote = ".") fast-forwards with a
// bare `git fetch . other:side` refspec, without a checkout.
func TestPullBranchLocalUpstream(t *testing.T) {
	dir := upstreamScopeRepo(t)
	c := pullBranchContext(t, dir)

	// "side" is not checked out; its upstream "other" has one extra commit.
	message, gerr := routeAction(c, "pull-branch", "side", nil)

	if gerr != nil {
		t.Fatalf("routeAction: %v (stderr: %v)", gerr.Message, gerr.Detail)
	}

	if !strings.Contains(message, "other") || !strings.Contains(message, "side") {
		t.Errorf("message = %q, want it to mention branch and upstream", message)
	}

	if side, other := headTip(t, dir, "refs/heads/side"), headTip(t, dir, "refs/heads/other"); side != other {
		t.Errorf("side tip %s != upstream tip %s", side, other)
	}
}

// A real remote also fast-forwards a non-checked-out local ref, updating
// both refs/heads/extra and refs/remotes/origin/main in one fetch.
func TestPullBranchRemoteUpstream(t *testing.T) {
	ctx := context.Background()
	origin, seed, work := t.TempDir(), t.TempDir(), t.TempDir()

	steps := [][]string{
		{"init", "-q", "-b", "main", seed},
		{"-C", seed, "config", "user.email", "test@example.com"},
		{"-C", seed, "config", "user.name", "Test"},
		{"-C", seed, "commit", "--allow-empty", "-m", "seed"},
		{"init", "-q", "--bare", origin},
		{"-C", seed, "push", "-q", origin, "main"},
		{"clone", "-q", origin, work},
	}

	for _, c := range steps {
		if _, err := RunGit(ctx, "", c...); err != nil {
			t.Fatalf("setup git %v failed: %v", c, err)
		}
	}

	// The work clone is deliberately stale: it has not fetched since the
	// clone. "extra" is a local branch beside HEAD that tracks origin/main.
	extra := [][]string{
		{"-C", work, "branch", "-q", "extra", "origin/main"},
		{"-C", work, "branch", "--set-upstream-to=origin/main", "extra"},
		{"-C", seed, "commit", "--allow-empty", "-m", "remote ahead"},
		{"-C", seed, "push", "-q", origin, "main"},
	}

	for _, c := range extra {
		if _, err := RunGit(ctx, "", c...); err != nil {
			t.Fatalf("setup git %v failed: %v", c, err)
		}
	}

	if got, want := headTip(t, work, "refs/heads/extra"), headTip(t, seed, "refs/heads/main"); got == want {
		t.Fatal("test setup: expected stale extra branch")
	}

	c := pullBranchContext(t, work)
	message, gerr := routeAction(c, "pull-branch", "extra", nil)

	if gerr != nil {
		t.Fatalf("routeAction: %v (stderr: %v)", gerr.Message, gerr.Detail)
	}

	if !strings.Contains(message, "origin/main") {
		t.Errorf("message = %q, want it to name the upstream", message)
	}

	if got, want := headTip(t, work, "refs/heads/extra"), headTip(t, seed, "refs/heads/main"); got != want {
		t.Errorf("extra %s did not fast-forward to origin tip %s", got, want)
	}
}

// Pulling a branch without an upstream surfaces a friendly error.
func TestPullBranchNoUpstream(t *testing.T) {
	dir := upstreamScopeRepo(t)
	c := pullBranchContext(t, dir)

	_, gerr := routeAction(c, "pull-branch", "other", nil)

	if gerr == nil {
		t.Fatal("expected error for branch without upstream")
	}

	if !strings.Contains(gerr.Message, "no upstream") {
		t.Errorf("message = %q, want upstream mention", gerr.Message)
	}
}

// Fetching into the checked-out branch is refused by git, so the backend
// routes HEAD's branch through the regular ff-only pull.
func TestPullBranchIntoCheckedOut(t *testing.T) {
	dir := upstreamScopeRepo(t)
	c := pullBranchContext(t, dir)

	message, gerr := routeAction(c, "pull-branch", "main", nil)

	if gerr != nil {
		t.Fatalf("routeAction: %v (stderr: %v)", gerr.Message, gerr.Detail)
	}

	if !strings.Contains(message, "Pulled latest changes") {
		t.Errorf("message = %q, want the regular pull wording", message)
	}
}
