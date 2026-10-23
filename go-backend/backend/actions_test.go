package backend

import (
	"strings"
	"testing"
)

func TestClassifyCherryPickConflictExtractsFiles(t *testing.T) {
	stderr := "error: could not apply 2022036... side-commit\n" +
		"hint: after resolving the conflicts, mark the corrected paths\n" +
		"CONFLICT (content): Merge conflict in a.txt\n" +
		"Auto-merging a.txt\n"
	err := ClassifyGitError(stderrOrStdout(stderr, ""), 1)

	if err.Code != ErrCodeMergeConflict {
		t.Fatalf("code = %q, want MergeConflict (stderr sample)", err.Code)
	}

	if len(err.Files) != 1 || err.Files[0] != "a.txt" {
		t.Fatalf("files = %v, want [a.txt]", err.Files)
	}
}

func TestClassifyNoUpstream(t *testing.T) {
	// Real stderr shape of `git push` on a branch that was never published.
	stderr := "fatal: The current branch feature/web-urls has no upstream branch.\n" +
		"To push the current branch and set the remote as upstream, use:\n" +
		"\n" +
		"    git push --set-upstream origin feature/web-urls\n"
	err := ClassifyGitError(stderrOrStdout(stderr, ""), 1)

	if err.Code != ErrCodeNoUpstream {
		t.Fatalf("code = %q, want NoUpstream", err.Code)
	}

	if !strings.Contains(err.Message, "upstream") {
		t.Fatalf("message = %q", err.Message)
	}

	if !strings.Contains(err.Detail, "feature/web-urls") {
		t.Fatalf("detail = %q, want the branch name", err.Detail)
	}
}

func TestCherryPickConflictFilesFromStdout(t *testing.T) {
	// Real-life shape: git writes CONFLICT lines to stdout, hints + the
	// "cherry-pick" keyword to stderr.
	stdout := "Auto-merging a.txt\nCONFLICT (content): Merge conflict in a.txt\n"
	stderr := "error: could not apply 2022036a... side-commit\n" +
		"hint: After resolving the conflicts, mark them with\n" +
		"hint: \"git add/rm <pathspec>\", then run\n" +
		"hint: \"git cherry-pick --continue\".\n"
	res := actionRunResult{stdout: stdout, stderr: stderr, exit: 1}
	// The runner side: classify then top up from both streams.
	gerr := ClassifyGitError(stderrOrStdout(res.stderr, res.stdout), res.exit)

	if gerr.Code != ErrCodeMergeConflict {
		t.Fatalf("code = %q, want MergeConflict", gerr.Code)
	}

	if len(gerr.Files) == 0 {
		gerr.Files = extractConflictFiles(res.stderr + "\n" + res.stdout)
	}

	if len(gerr.Files) != 1 || gerr.Files[0] != "a.txt" {
		t.Fatalf("files = %v, want [a.txt]", gerr.Files)
	}
}

func TestConflicParseRoundTrip(t *testing.T) {
	content := "line1\n<<<<<<< OURS\nours line\n||||||| BASE\nbase line\n=======\ntheirs line\n>>>>>>> THEIRS\nline2\n"
	parsed := parseConflictMarkers(content)

	if !parsed.hasMarkers {
		t.Fatal("hasMarkers = false")
	}

	if len(parsed.blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(parsed.blocks))
	}

	block := parsed.blocks[0]

	if block.ours != "ours line" || block.theirs != "theirs line" || block.base != "base line" {
		t.Fatalf("block sides = %q/%q/%q", block.ours, block.base, block.theirs)
	}

	if block.startLine != 2 || block.endLine != 8 {
		t.Fatalf("lines = %d..%d, want 2..8", block.startLine, block.endLine)
	}

	updated, err := applyConflictResolution(content, 0, "theirs")

	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(updated, "<<<<<<<") || strings.Contains(updated, "ours line") {
		t.Fatalf("theirs replacement left ours/markers behind: %q", updated)
	}

	if !strings.Contains(updated, "theirs line") || !strings.Contains(updated, "line1") {
		t.Fatalf("theirs replacement lost content: %q", updated)
	}

	// after ours-replacement no markers remain.
	updatedOurs, err := applyConflictResolution(content, 0, "ours")

	if err != nil {
		t.Fatal(err)
	}

	if parseConflictMarkers(updatedOurs).hasMarkers {
		t.Fatalf("ours replacement still has markers: %q", updatedOurs)
	}
}

func TestJournalLabelAndCapture(t *testing.T) {
	if got := journalLabel("commit", "", ""); got != "Commit" {
		t.Fatalf("commit label = %q", got)
	}

	if got := journalLabel("merge", "main", ""); got != "Merge main" {
		t.Fatalf("merge label = %q", got)
	}

	if got := journalLabel("cherry-pick", "abcdef123456", ""); got != "Cherry-pick abcdef1" {
		t.Fatalf("cherry-pick label = %q", got)
	}

	if got := journalLabel("create-branch", "", "feat"); got != "Create branch feat" {
		t.Fatalf("create-branch label = %q", got)
	}

	if got := journalLabel("unknown-kind", "", ""); got != "unknown-kind" {
		t.Fatalf("unknown label = %q", got)
	}

	if journalCaptureFor("stage") != journalCaptureIndex {
		t.Fatal("stage should capture Index")
	}

	if journalCaptureFor("merge") != journalCaptureHead {
		t.Fatal("merge should capture Head")
	}

	if journalCaptureFor("push") != journalCaptureNone {
		t.Fatal("push should not capture")
	}

	if !isJournalableAction("commit") || isJournalableAction("push") {
		t.Fatal("journalable predicate wrong")
	}
}

func TestUndoRedoSafetyFlags(t *testing.T) {
	state := RepoState{Merging: true}

	if isUndoRedoSafe(state) {
		t.Fatal("merging should block undo/redo")
	}

	state.Merging = false

	if !isUndoRedoSafe(state) {
		t.Fatal("clean state should allow undo/redo")
	}
}

func TestOptionLikeRejects(t *testing.T) {
	if err := rejectOptionLike("-h", "target"); err == nil {
		t.Fatal("-h must be rejected")
	}

	if err := rejectOptionLike("", "target"); err == nil {
		t.Fatal("empty must be rejected")
	}

	if err := rejectOptionLike("ma\nin", "target"); err == nil {
		t.Fatal("newline must be rejected")
	}

	if err := rejectOptionLike("feature/x", "branch"); err != nil {
		t.Fatal("normal names must pass")
	}

	if _, err := checkedRev("HEAD~1", "rev"); err != nil {
		t.Fatalf("HEAD~1 must pass: %v", err)
	}
}

func TestRebaseBundleWritesTodoAndCleansUp(t *testing.T) {
	bundle, gerr := createRebaseEditorBundle([]rebaseTodoEntry{
		{action: "pick", sha: "abc123", subject: "first"},
		{action: "reword", sha: "def456", subject: "second", message: "Better title"},
	})

	if gerr != nil {
		t.Fatalf("bundle: %v", gerr)
	}

	todo, err := osReadFile(bundle.dir + "/todo")

	if err != nil {
		t.Fatal(err)
	}

	want := "pick abc123 first\nreword def456 second\n"

	if string(todo) != want {
		t.Fatalf("todo = %q, want %q", todo, want)
	}

	if _, err := osReadFile(bundle.dir + "/msgs/msg-0"); err != nil {
		t.Fatalf("reword msgs: %v", err)
	}

	if bundle.env["GIT_SEQUENCE_EDITOR"] == "" || bundle.env["GIT_EDITOR"] == "" {
		t.Fatalf("editors missing: %v", bundle.env)
	}

	bundle.cleanup()

	if _, err := osStatPath(bundle.dir); !osIsNotExist(err) {
		t.Fatalf("bundle dir not removed: %v", err)
	}
}

func TestFormatGitCommandQuotes(t *testing.T) {
	got := formatGitCommand([]string{"commit", "-m", "two words"})

	if got != `git commit -m "two words"` {
		t.Fatalf("format = %q", got)
	}
}

func TestTruncateForLogBoundaries(t *testing.T) {
	short := "abc"
	out, truncated := truncateForLog(short)

	if truncated || out != short {
		t.Fatal("short text must pass through")
	}

	long := strings.Repeat("x", maxCommandLogChars+50)
	out, truncated = truncateForLog(long)

	if !truncated || len(out) > maxCommandLogChars+3 { // + "…" (3 UTF-8 bytes)
		t.Fatalf("truncation failed: %d/%v", len(out), truncated)
	}
}

func TestRebaseCommitLogParsing(t *testing.T) {
	log := "a1\x1fa1 shorter\x1fsubject one\x1fT\x1f1760000000\x1e" +
		"b2\x1fb2 short\x1fsubject two\x1fU\x1f1760000300\x1e"
	entries := parseRebaseCommitLog(log)

	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}

	first := entries[0]

	if first.Sha != "a1" || first.ShortSha != "a1 shorter" || first.Subject != "subject one" {
		t.Fatalf("first = %+v", first)
	}

	if first.Date == "" || first.Date[0] != '2' {
		t.Fatalf("date not ISO: %q", first.Date)
	}

	if empty := parseRebaseCommitLog(""); len(empty) != 0 {
		t.Fatal("empty log must give zero entries")
	}
}

func TestImageMimeExtensionSplit(t *testing.T) {
	if got := imageMime("dir/a.PNG"); got != "image/png" {
		t.Fatalf("dotless-split failed: %q", got)
	}

	if got := imageMime("dir.a/b.png"); got != "image/png" {
		t.Fatalf("nested ext failed: %q", got)
	}

	if got := imageMime("a.svg"); got != "image/svg+xml" {
		t.Fatalf("svg failed: %q", got)
	}

	// Extensionless: the whole name is the "extension" (as in Rust rsplit).
	if got := imageMime("png"); got != "image/png" {
		t.Fatalf("extensionless png name: %q", got)
	}

	if got := imageMime("dir.b/noext"); got != "" {
		t.Fatalf("dot-in-dir path: %q", got)
	}

	if got := imageMime("a.txt"); got != "" {
		t.Fatalf("txt must not map: %q", got)
	}
}

func TestValidateShaPortRules(t *testing.T) {
	for _, ok := range []string{"HEAD", "abcABC012", "main", "HEAD~1", "stash@{0}"} {
		if _, err := validateShaPort(ok); err != nil {
			t.Fatalf("%q rejected: %v", ok, err)
		}
	}

	for _, bad := range []string{"a:b", "a b", "HEAD..x"} {
		if _, err := validateShaPort(bad); err == nil {
			t.Fatalf("%q accepted, want reject", bad)
		}
	}

	if out, err := validateShaPort("  HEAD  "); err != nil || out != "HEAD" {
		t.Fatalf("trim failed: %q/%v", out, err)
	}

	if out, err := validateShaPort(""); err != nil || out != "" {
		t.Fatal("empty must pass through empty")
	}
}

func TestConflictWireBlocksOmitEmptyBase(t *testing.T) {
	content := "<<<<<<< ours-label\no\n=======\nt\n>>>>>>> theirs-label\n"
	parsed := parseConflictMarkers(content)
	wire := wireBlocks(parsed.blocks)

	if len(wire) != 1 {
		t.Fatalf("blocks = %d", len(wire))
	}

	if wire[0].Base != nil {
		t.Fatal("base must be nil when no ||||||| section")
	}

	if wire[0].Ours != "o" || wire[0].Theirs != "t" || wire[0].OursLabel != "ours-label" {
		t.Fatalf("sides wrong: %+v", wire[0])
	}

	// base round trip through the ||||||| section.
	withBase := "<<<<<<< a\no\n||||||| b\nbase\n=======\nt\n>>>>>>> c\n"
	wb := wireBlocks(parseConflictMarkers(withBase).blocks)

	if wb[0].Base == nil || *wb[0].Base != "base" {
		t.Fatalf("base version lost: %+v", wb[0])
	}
}
