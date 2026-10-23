package backend

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Wire models. Field names MUST match `shared/git/models.ts` (camelCase);
// TS renders revive them into domain types via wireAdapter.

type WireFileStatus = string

const (
	StatusModified   = "modified"
	StatusAdded      = "added"
	StatusDeleted    = "deleted"
	StatusRenamed    = "renamed"
	StatusCopied     = "copied"
	StatusUntracked  = "untracked"
	StatusIgnored    = "ignored"
	StatusConflicted = "conflicted"
)

type WireRefType = string

const (
	RefHead         = "head"
	RefLocalBranch  = "localBranch"
	RefRemoteBranch = "remoteBranch"
	RefTag          = "tag"
	RefStash        = "stash"
)

type WireRef struct {
	Type   WireRefType `json:"type"`
	Name   string      `json:"name"`
	Label  string      `json:"label"`
	IsHead *bool       `json:"isHead,omitempty"`
}

type WireAuthor struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatarUrl"`
	Initials  string `json:"initials"`
	Color     string `json:"color"`
}

type WireCommit struct {
	Sha         string     `json:"sha"`
	ShortSha    string     `json:"shortSha"`
	Parents     []string   `json:"parents"`
	Subject     string     `json:"subject"`
	Body        string     `json:"body,omitempty"`
	Author      WireAuthor `json:"author"`
	Committer   WireAuthor `json:"committer"`
	Date        string     `json:"date"`
	Refs        []WireRef  `json:"refs"`
	IsMerge     bool       `json:"isMerge"`
	IsStashBase *bool      `json:"isStashBase,omitempty"`
}

type WireBranch struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"` // 'local' | 'remote'
	Group        string  `json:"group"`
	Remote       *string `json:"remote,omitempty"`
	TipSha       string  `json:"tipSha"`
	Ahead        int     `json:"ahead"`
	Behind       int     `json:"behind"`
	IsCurrent    bool    `json:"isCurrent"`
	IsFavorite   bool    `json:"isFavorite"`
	LastActivity string  `json:"lastActivity"`
	LaneColorIdx int     `json:"laneColorIndex"`
	Upstream     *string `json:"upstream,omitempty"`
}

type WireTag struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Sha       string `json:"sha"`
	Message   string `json:"message"`
	Date      string `json:"date"`
	Annotated bool   `json:"annotated"`
}

type WireStash struct {
	ID           string  `json:"id"`
	Index        int     `json:"index"`
	Message      string  `json:"message"`
	Branch       string  `json:"branch"`
	Date         string  `json:"date"`
	FilesChanged int     `json:"filesChanged"`
	BaseSha      *string `json:"baseSha"`
	BaseSubject  *string `json:"baseSubject,omitempty"`
}

type WireWorktree struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	IsMain   bool   `json:"isMain"`
	IsLocked bool   `json:"isLocked"`
}

type WireRemote struct {
	Name     string `json:"name"`
	FetchURL string `json:"fetchUrl"`
	PushURL  string `json:"pushUrl"`
}

type RepoState struct {
	DetachedHead   bool    `json:"detachedHead"`
	Merging        bool    `json:"merging"`
	Rebasing       bool    `json:"rebasing"`
	CherryPicking  bool    `json:"cherryPicking"`
	Reverting      bool    `json:"reverting"`
	Bisecting      bool    `json:"bisecting"`
	OperationLabel *string `json:"operationLabel"`
}

type WireRepository struct {
	Name          string       `json:"name"`
	Path          string       `json:"path"`
	GitVersion    string       `json:"gitVersion"`
	CurrentBranch string       `json:"currentBranch"`
	Detached      bool         `json:"detached"`
	HeadSha       string       `json:"headSha"`
	Ahead         int          `json:"ahead"`
	Behind        int          `json:"behind"`
	RemoteURL     string       `json:"remoteUrl"`
	Remotes       []WireRemote `json:"remotes"`
	State         RepoState    `json:"state"`
	CanUndo       bool         `json:"canUndo"`
	CanRedo       bool         `json:"canRedo"`
	UndoLabel     *string      `json:"undoLabel"`
	RedoLabel     *string      `json:"redoLabel"`
}

type CommitPageInfo struct {
	OldestSha *string `json:"oldestSha"`
	HasMore   bool    `json:"hasMore"`
	Total     *int    `json:"total"`
}

type WireRepositorySnapshot struct {
	Repository   WireRepository        `json:"repository"`
	Commits      []WireCommit          `json:"commits"`
	Page         CommitPageInfo        `json:"page"`
	Branches     []WireBranch          `json:"branches"`
	Tags         []WireTag             `json:"tags"`
	Stashes      []WireStash           `json:"stashes"`
	Worktrees    []WireWorktree        `json:"worktrees"`
	WorkingTree  []WireWorkingTreeFile `json:"workingTree"`
	Authors      []WireAuthor          `json:"authors"`
	CommitAuthor WireAuthor            `json:"commitAuthor"`
}

type WireWorkingTreeFile struct {
	ID          string         `json:"id"`
	Path        string         `json:"path"`
	FileName    string         `json:"fileName"`
	Directory   string         `json:"directory"`
	Status      WireFileStatus `json:"status"`
	Staged      bool           `json:"staged"`
	Additions   int            `json:"additions"`
	Deletions   int            `json:"deletions"`
	OldPath     string         `json:"oldPath,omitempty"`
	IsSubmodule bool           `json:"isSubmodule,omitempty"`
}

/* Diff models */

type DiffResult struct {
	Path      string     `json:"path"`
	OldPath   string     `json:"oldPath,omitempty"`
	Binary    bool       `json:"binary"`
	TooLarge  bool       `json:"tooLarge"`
	Language  string     `json:"language"`
	Hunks     []DiffHunk `json:"hunks"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
}

type DiffHunk struct {
	Header   string     `json:"header"`
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []DiffLine `json:"lines"`
}

type DiffLine struct {
	Type    string `json:"type"` // 'context'|'add'|'del'|'hunk'|'meta'
	Content string `json:"content"`
	OldLine *int   `json:"oldLine"`
	NewLine *int   `json:"newLine"`
}

/* Commit files */

type CommitFilesResult = []WireWorkingTreeFile

/* Search */

type SearchQuery struct {
	Text        string
	Fields      []string
	SearchRegex bool
	Limit       int
	GraphScope  string
}

type SearchQueryWire struct {
	Text       string   `json:"text"`
	Fields     []string `json:"fields,omitempty"`
	Regex      bool     `json:"regex,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	GraphScope string   `json:"graphScope,omitempty"`
}

type SearchCommitHit struct {
	Sha      string `json:"sha"`
	ShortSha string `json:"shortSha"`
	Subject  string `json:"subject"`
	Author   string `json:"author"`
	Date     string `json:"date"`
}

type SearchResults struct {
	Commits []SearchCommitHit `json:"commits"`
	Files   []string          `json:"files"`
}

/* Serialized errors (mirror shared/git/errors.ts SerializedGitError) */

type SerializedGitError struct {
	GitError bool     `json:"__gitError"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Detail   string   `json:"detail,omitempty"`
	Files    []string `json:"files,omitempty"`
}

/* Recents / open */

type OpenRepoResult struct {
	OK       bool                    `json:"ok"`
	Snapshot *WireRepositorySnapshot `json:"snapshot,omitempty"`
	Error    *SerializedGitError     `json:"error,omitempty"`
}

type ActionEnvelope struct {
	OK        bool                `json:"ok"`
	Message   string              `json:"message"`
	Error     *SerializedGitError `json:"error,omitempty"`
	Conflicts []string            `json:"conflicts,omitempty"`
	// Every git invocation the action ran, in order (renders the command log).
	Commands []ExecutedGitCommand `json:"commands,omitempty"`
}

const (
	ErrCodeMergeConflict      = "MergeConflict"
	ErrCodeAuthentication     = "Authentication"
	ErrCodeRepositoryNotFound = "RepositoryNotFound"
	ErrCodeNotARepository     = "NotARepository"
	ErrCodeBareRepository     = "BareRepository"
	ErrCodeDetachedHead       = "DetachedHead"
	ErrCodeRemoteRejected     = "RemoteRejected"
	ErrCodeNoUpstream         = "NoUpstream"
	ErrCodeNothingToCommit    = "NothingToCommit"
	ErrCodeNetwork            = "Network"
	ErrCodeDirtyWorktree      = "DirtyWorktree"
	ErrCodeCancelled          = "Cancelled"
	ErrCodeUnknown            = "Unknown"
)

/* ---------------------- date helpers (parse_git_date) ---------------------- */

const epochISO = "1970-01-01T00:00:00.000Z"

func formatISO(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// parseGitDate mirrors the Rust `parse_git_date`: accepts epoch seconds or any
// RFC3339-ish input (including garbled "+0000"-style offsets) and normalizes
// to millisecond ISO-8601 UTC. Malformed input falls back to the epoch.
func parseGitDate(raw string) string {
	s := strings.TrimSpace(raw)

	if s == "" {
		return epochISO
	}

	if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
		return formatISO(time.Unix(secs, 0))
	}

	// Garbled 5-char offset "+0000" → "+00:00".
	if len(s) >= 5 {
		cut := len(s) - 5
		tail := s[cut:]

		if (tail[0] == '+' || tail[0] == '-') && isAllDigits(tail[1:]) {
			s = s[:cut] + tail[:3] + ":" + tail[3:]
		}
	}

	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return formatISO(t)
		}
	}

	return epochISO
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return len(s) > 0
}

// splitFilePath returns (fileName, directory) exactly like the Rust/TS pair.
func splitFilePath(full string) (string, string) {
	if idx := strings.LastIndex(full, "/"); idx >= 0 {
		return full[idx+1:], full[:idx]
	}

	return full, ""
}

func formatUnixISO(seconds int64) string {
	return formatISO(time.Unix(seconds, 0))
}

func intPtr(i int) *int { return &i }

func strPtr(s string) *string { return &s }

var _ = fmt.Sprintf // keep fmt for debug edits later
