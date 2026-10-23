package backend

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
)

/* Undo/redo journal (port of actions.rs `Journal`).

Each repository keeps its own undo/redo stacks of checkpoints. A checkpoint
captures HEAD + current branch, and for index-scoped actions also the index as
a tree object (`git write-tree`). Undo restores the checkpoint's HEAD/branch,
optionally replaying the index tree. The stack is lost when the backend quits
(like the Rust main-process journal), and cleared on close. */

type JournalCapture int

const (
	journalCaptureNone JournalCapture = iota
	journalCaptureHead
	journalCaptureIndex
)

type Checkpoint struct {
	// HeadSha before the action.
	HeadSha string
	// Branch name, or "HEAD" when detached.
	Branch string
	// Index tree captured with `git write-tree` ("" when Head-scoped).
	IndexTreeSha string
	// Reset mode to apply on restore when the caller was a reset action.
	ResetMode string
}

type journalEntry struct {
	checkpoint Checkpoint
	label      string
}

type Journal struct {
	mu   sync.Mutex
	undo []journalEntry
	redo []journalEntry
}

type journalsHub struct {
	mu   sync.Mutex
	byPK map[string]*Journal
}

var journals = &journalsHub{byPK: map[string]*Journal{}}

// journalFor returns the journal of one repository (canonical abs path).
func journalFor(repoPath string) *Journal {
	abs, err := filepath.Abs(repoPath)

	if err != nil {
		return &Journal{}
	}

	journals.mu.Lock()
	defer journals.mu.Unlock()
	j, ok := journals.byPK[abs]

	if !ok {
		j = &Journal{}
		journals.byPK[abs] = j
	}

	return j
}

// clearJournal drops undo/redo state (on repository close).
func clearJournal(repoPath string) {
	if abs, err := filepath.Abs(repoPath); err == nil {
		journals.mu.Lock()
		delete(journals.byPK, abs)
		journals.mu.Unlock()
	}
}

// JournalStateWire mirrors JournalState in actions.rs (camelCase wire).
type JournalStateWire struct {
	CanUndo   bool    `json:"canUndo"`
	CanRedo   bool    `json:"canRedo"`
	UndoLabel *string `json:"undoLabel"`
	RedoLabel *string `json:"redoLabel"`
}

func (j *Journal) State(state RepoState) JournalStateWire {
	j.mu.Lock()
	defer j.mu.Unlock()
	safe := isUndoRedoSafe(state)
	wire := JournalStateWire{}

	if safe {
		if len(j.undo) > 0 {
			wire.CanUndo = true
			wire.UndoLabel = strPtr(j.undo[len(j.undo)-1].label)
		}

		if len(j.redo) > 0 {
			wire.CanRedo = true
			wire.RedoLabel = strPtr(j.redo[len(j.redo)-1].label)
		}
	}

	return wire
}

func (j *Journal) PushUndo(cp Checkpoint, label string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.undo = append(j.undo, journalEntry{checkpoint: cp, label: label})
	j.redo = nil
}

// Capture mirrors Journal::capture: HEAD sha + branch, optional index tree.
func (j *Journal) Capture(ctx context.Context, cwd string, cap JournalCapture) (Checkpoint, *GitError) {
	headRes, err := RunGit(ctx, cwd, "rev-parse", "HEAD")

	if err != nil {
		return Checkpoint{}, unknownErr("failed to spawn git")
	}

	head := strings.TrimSpace(headRes.Stdout)

	if headRes.Exit != 0 || head == "" {
		return Checkpoint{}, unknownErr("Could not read HEAD.")
	}

	branchRes, _ := RunGit(ctx, cwd, "rev-parse", "--abbrev-ref", "HEAD")
	branch := strings.TrimSpace(branchRes.Stdout)

	if branch == "" {
		branch = "HEAD"
	}

	cp := Checkpoint{HeadSha: head, Branch: branch}

	if cap == journalCaptureIndex {
		treeRes, _ := RunGit(ctx, cwd, "write-tree")

		if tree := strings.TrimSpace(treeRes.Stdout); tree != "" {
			cp.IndexTreeSha = tree
		}
	}

	return cp, nil
}

// Undo pops the last checkpoint and restores it (pushing the pre-restore
// state onto the redo stack).
func (j *Journal) Undo(ctx context.Context, cwd string) (string, *GitError) {
	j.mu.Lock()

	if len(j.undo) == 0 {
		j.mu.Unlock()

		return "", unknownErr("Nothing to undo.")
	}

	entry := j.undo[len(j.undo)-1]
	j.undo = j.undo[:len(j.undo)-1]
	cap := journalCaptureHead

	if entry.checkpoint.IndexTreeSha != "" {
		cap = journalCaptureIndex
	}

	j.mu.Unlock()
	current, gerr := j.Capture(ctx, cwd, cap)

	if gerr != nil {
		return "", gerr
	}

	j.mu.Lock()
	j.redo = append(j.redo, journalEntry{checkpoint: current, label: entry.label})
	j.mu.Unlock()

	if gerr := restoreCheckpoint(ctx, cwd, entry.checkpoint); gerr != nil {
		return "", gerr
	}

	return "Undid " + entry.label, nil
}

// Redo mirrors Undo in the opposite direction.
func (j *Journal) Redo(ctx context.Context, cwd string) (string, *GitError) {
	j.mu.Lock()

	if len(j.redo) == 0 {
		j.mu.Unlock()

		return "", unknownErr("Nothing to redo.")
	}

	entry := j.redo[len(j.redo)-1]
	j.redo = j.redo[:len(j.redo)-1]
	cap := journalCaptureHead

	if entry.checkpoint.IndexTreeSha != "" {
		cap = journalCaptureIndex
	}

	j.mu.Unlock()
	current, gerr := j.Capture(ctx, cwd, cap)

	if gerr != nil {
		return "", gerr
	}

	j.mu.Lock()
	j.undo = append(j.undo, journalEntry{checkpoint: current, label: entry.label})
	j.mu.Unlock()

	if gerr := restoreCheckpoint(ctx, cwd, entry.checkpoint); gerr != nil {
		return "", gerr
	}

	return "Redid " + entry.label, nil
}

// restoreCheckpoint mirrors Journal::restore: prefer read-tree for index
// checkpoints while HEAD is unchanged; otherwise re-checkout the branch (or
// detach) and reset to the captured sha.
func restoreCheckpoint(ctx context.Context, cwd string, cp Checkpoint) *GitError {
	head, gerr := journalHead(ctx, cwd)

	if gerr != nil {
		return gerr
	}

	if cp.IndexTreeSha != "" && cp.HeadSha == head {
		if _, gerr := runActionGit(ctx, cwd, runOpts{args: []string{"read-tree", cp.IndexTreeSha}}); gerr != nil {
			return gerr
		}

		return nil
	}

	resetFlag := "--hard"

	switch cp.ResetMode {
	case "soft":
		resetFlag = "--soft"

	case "mixed":
		resetFlag = "--mixed"
	}

	if cp.Branch != "" && cp.Branch != "HEAD" {
		res, gerr := runActionGit(ctx, cwd, runOpts{
			args:         []string{"checkout", cp.Branch},
			allowFailure: true, // tolerate a missing branch; fall through to detach
		})

		if gerr != nil {
			return gerr
		}

		if res.exit == 0 {
			if _, gerr := runActionGit(ctx, cwd, runOpts{args: []string{"reset", resetFlag, cp.HeadSha}}); gerr != nil {
				return gerr
			}

			if cp.IndexTreeSha != "" && resetFlag == "--hard" {
				if _, gerr := runActionGit(ctx, cwd, runOpts{args: []string{"read-tree", cp.IndexTreeSha}}); gerr != nil {
					return gerr
				}
			}

			return nil
		}
	}

	if _, gerr := runActionGit(ctx, cwd, runOpts{args: []string{"checkout", "--detach", cp.HeadSha}}); gerr != nil {
		return gerr
	}

	if cp.IndexTreeSha != "" {
		if _, gerr := runActionGit(ctx, cwd, runOpts{args: []string{"read-tree", cp.IndexTreeSha}}); gerr != nil {
			return gerr
		}
	}

	return nil
}

func journalHead(ctx context.Context, cwd string) (string, *GitError) {
	res, err := RunGit(ctx, cwd, "rev-parse", "HEAD")

	if err != nil {
		return "", unknownErr("failed to spawn git")
	}

	if !res.Ok() {
		return "", unknownErr("Could not read HEAD.")
	}

	return strings.TrimSpace(res.Stdout), nil
}

// isUndoRedoSafe mirrors actions.rs: no undo/redo during an in-progress
// git operation.
func isUndoRedoSafe(state RepoState) bool {
	return !state.Merging && !state.Rebasing && !state.CherryPicking &&
		!state.Reverting && !state.Bisecting
}

// journalCaptureFor mirrors journal_capture_for / shared policy
// (`shared/git/undoPolicy.ts`).
func journalCaptureFor(kind string) JournalCapture {
	switch kind {
	case "commit", "amend", "commit-and-push", "commit-and-force-push",
		"merge", "merge-continue", "rebase", "rebase-from-here",
		"interactive-rebase", "rebase-continue", "cherry-pick",
		"cherry-pick-continue", "revert", "revert-continue",
		"reset-soft", "reset-mixed", "reset-hard",
		"checkout", "checkout-commit", "checkout-tag",
		"create-branch", "pop-stash", "apply-stash":
		return journalCaptureHead

	case "stash", "stage", "unstage", "stage-all", "unstage-all",
		"stage-patch", "unstage-patch",
		"resolve-conflict-ours", "resolve-conflict-theirs",
		"resolve-conflict-block", "mark-conflict-resolved":
		return journalCaptureIndex

	default:
		return journalCaptureNone
	}
}

func isJournalableAction(kind string) bool {
	return journalCaptureFor(kind) != journalCaptureNone
}

// journalLabel mirrors journal_label in actions.rs.
func journalLabel(kind, target, metaName string) string {
	t := target
	hasTarget := target != ""

	switch kind {
	case "commit":
		return "Commit"

	case "amend":
		return "Amend commit"

	case "commit-and-push":
		return "Commit and push"

	case "commit-and-force-push":
		return "Commit and force push"

	case "merge", "merge-continue":
		if hasTarget {
			return "Merge " + t
		}

		return "Merge"

	case "rebase", "rebase-continue":
		if hasTarget {
			return "Rebase onto " + t
		}

		return "Rebase"

	case "rebase-from-here":
		return "Rebase from commit"

	case "interactive-rebase":
		return "Interactive rebase"

	case "cherry-pick", "cherry-pick-continue":
		if hasTarget {
			return "Cherry-pick " + truncateSha(t, 7)
		}

		return "Cherry-pick"

	case "revert", "revert-continue":
		if hasTarget {
			return "Revert " + truncateSha(t, 7)
		}

		return "Revert"

	case "reset-soft":
		return "Soft reset"

	case "reset-mixed":
		return "Mixed reset"

	case "reset-hard":
		return "Hard reset"

	case "checkout":
		if hasTarget {
			return "Checkout " + t
		}

		return "Checkout"

	case "checkout-commit":
		if hasTarget {
			return "Checkout " + truncateSha(t, 7)
		}

		return "Checkout commit"

	case "checkout-tag":
		if hasTarget {
			return "Checkout tag " + t
		}

		return "Checkout tag"

	case "create-branch":
		name := metaName

		if name == "" {
			name = target
		}

		if name == "" {
			return "Create branch"
		}

		return "Create branch " + name

	case "stash":
		return "Stash changes"

	case "pop-stash":
		return "Pop stash"

	case "apply-stash":
		return "Apply stash"

	case "stage", "stage-patch":
		if hasTarget {
			return "Stage " + t
		}

		return "Stage file"

	case "unstage", "unstage-patch":
		if hasTarget {
			return "Unstage " + t
		}

		return "Unstage file"

	case "stage-all":
		return "Stage all"

	case "unstage-all":
		return "Unstage all"

	case "resolve-conflict-ours", "resolve-conflict-theirs",
		"resolve-conflict-block", "mark-conflict-resolved":
		if hasTarget {
			return "Resolve " + t
		}

		return "Resolve conflict"

	default:
		return kind
	}
}
