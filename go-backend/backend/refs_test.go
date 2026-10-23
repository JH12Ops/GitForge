package backend

import (
	"strings"
	"testing"
)

func TestParseBranchesRemoteTracking(t *testing.T) {
	// Field order mirrors branchesCommand: refname, sha, HEAD,
	// upstream:short, upstream:track, committerdate:unix.
	lines := []string{
		strings.Join([]string{"refs/heads/main", "aaa", "*", "origin/main", "[ahead 2, behind 1]", "1000"}, refFIELD),
		strings.Join([]string{"refs/heads/feature", "ccc", "", "", "", "800"}, refFIELD),
		strings.Join([]string{"refs/remotes/origin/main", "bbb", "", "", "", "900"}, refFIELD),
		strings.Join([]string{"refs/remotes/origin/feature", "ccc", "", "", "", "800"}, refFIELD),
		strings.Join([]string{"refs/remotes/origin/HEAD", "bbb", "", "", "", "900"}, refFIELD),
	}
	branches := parseBranches(strings.Join(lines, "\n"), nil)

	byID := map[string]WireBranch{}

	for _, b := range branches {
		byID[b.ID] = b
	}

	local, ok := byID["local:main"]

	if !ok {
		t.Fatal("missing local main")
	}

	if local.Ahead != 2 || local.Behind != 1 {
		t.Errorf("local main ahead/behind = %d/%d, want 2/1", local.Ahead, local.Behind)
	}

	remote, ok := byID["remote:origin/main"]

	if !ok {
		t.Fatal("missing remote origin/main")
	}

	// Inverted tracking: local is ahead 2 / behind 1, so the remote is
	// 1 ahead and 2 behind relative to the local tip.
	if remote.Ahead != 1 || remote.Behind != 2 {
		t.Errorf("remote origin/main ahead/behind = %d/%d, want 1/2", remote.Ahead, remote.Behind)
	}

	untracked, ok := byID["remote:origin/feature"]

	if !ok {
		t.Fatal("missing remote origin/feature")
	}

	if untracked.Ahead != 0 || untracked.Behind != 0 {
		t.Errorf("remote without local counterpart ahead/behind = %d/%d, want 0/0", untracked.Ahead, untracked.Behind)
	}

	if _, ok := byID["remote:origin/HEAD"]; ok {
		t.Error("origin/HEAD symref should be skipped")
	}
}

func TestParseBranchesRemoteTrackingGoneUpstream(t *testing.T) {
	// `[gone]` upstreams are cleared and must not mark the remote row.
	lines := []string{
		strings.Join([]string{"refs/heads/main", "aaa", "*", "origin/main", "[gone]", "1000"}, refFIELD),
		strings.Join([]string{"refs/remotes/origin/main", "bbb", "", "", "", "900"}, refFIELD),
	}
	branches := parseBranches(strings.Join(lines, "\n"), nil)

	for _, b := range branches {
		if b.Kind != "remote" {
			continue
		}

		if b.Ahead != 0 || b.Behind != 0 {
			t.Errorf("remote branch %s ahead/behind = %d/%d, want 0/0 for gone upstream", b.ID, b.Ahead, b.Behind)
		}
	}
}
