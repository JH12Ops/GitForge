package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

/* Mirror of diff.rs: fetch_diff, parse_unified_diff, commit/compare files. */

// DiffRequestWire mirrors models.ts DiffRequest; repo path is passed at top
// level by WailsGitService alongside it.
type DiffRequestWire struct {
	Path             string  `json:"path"`
	Source           string  `json:"source"` // worktree|index|commit|range
	Sha              *string `json:"sha,omitempty"`
	ToSha            *string `json:"toSha,omitempty"`
	IgnoreWhitespace bool    `json:"ignoreWhitespace,omitempty"`
}

func assertPathInsideRepo(repoPath, path string) (string, *GitError) {
	clean := filepath.Clean(strings.TrimSpace(path))

	if clean == "" || clean == "." || strings.HasPrefix(clean, "../") ||
		filepath.IsAbs(clean) || strings.Contains(clean, "..") {
		return "", gitErr(ErrCodeUnknown, fmt.Sprintf("path %q escapes the repository", path), nil)
	}

	// Guard: file "<repo>/<path>" must not cross outside via symlinks is
	// tolerated for V1; the worktree diff command scopes by pathspec anyway.
	return clean, nil
}

// FetchDiff mirrors diff.rs fetch_diff.
func FetchDiff(ctx context.Context, repoPath string, req DiffRequestWire) (*DiffResult, *GitError) {
	safe, gerr := assertPathInsideRepo(repoPath, req.Path)

	if gerr != nil {
		return nil, gerr
	}

	if req.Source == "worktree" {
		if tooLarge, _ := fileSizeExceeds(repoPath, safe, 512*1024); tooLarge && !req.IgnoreWhitespace {
			result := parseUnifiedDiff(safe, "")
			result.TooLarge = true

			return &result, nil
		}
	}

	var args []string

	switch req.Source {
	case "index":
		args = []string{"diff", "--cached", "--", safe}

	case "commit":
		sha := deref(req.Sha, "HEAD")
		parents := commitParents(repoPath, sha)

		switch {
		case len(parents) > 1:
			args = []string{"diff", "-m", "--first-parent", sha + "^1", sha, "--", safe}

		case len(parents) == 1:
			args = []string{"diff", sha + "^", sha, "--", safe}

		default:
			args = []string{"show", sha, "--", safe}
		}

	case "range":
		args = []string{"diff", deref(req.Sha, "HEAD~1"), deref(req.ToSha, "HEAD"), "--", safe}

	default: // worktree
		ensureIntentToAdd(repoPath, safe)
		args = []string{"diff", "--", safe}
	}

	if req.IgnoreWhitespace {
		args = append([]string{args[0], "-w"}, args[1:]...)
	}

	res, err := RunGit(ctx, repoPath, args...)

	if err != nil || !res.Ok() {
		return nil, ClassifyGitError(res.Stderr, res.Exit)
	}

	result := parseUnifiedDiff(safe, res.Stdout)

	return &result, nil
}

func deref(s *string, fallback string) string {
	if s != nil && strings.TrimSpace(*s) != "" {
		return *s
	}

	return fallback
}

func fileSizeExceeds(repoPath, rel string, limit int64) (bool, int64) {
	fi, err := os.Stat(filepath.Join(repoPath, rel))

	if err != nil {
		return false, 0
	}

	return fi.Size() > limit, fi.Size()
}

func commitParents(repoPath, sha string) []string {
	res, _ := RunGit(context.Background(), repoPath, "rev-list", "--parents", "-n", "1", sha)

	if !res.Ok() {
		return nil
	}

	fields := strings.Fields(res.Stdout)

	if len(fields) <= 1 {
		return nil
	}

	return fields[1:]
}

// ensureIntentToAdd mirrors diff.rs: untracked files only appear in
// `git diff` when marked intent-to-add.
func ensureIntentToAdd(repoPath, path string) {
	tracked, _ := RunGit(context.Background(), repoPath, "ls-files", "--error-unmatch", "--", path)

	if tracked.Ok() {
		return
	}

	_, _ = RunGit(context.Background(), repoPath, "add", "-N", "--", path)
}

// parseUnifiedDiff mirrors diff.rs parse_unified_diff (see that function for
// the line-type contract; TS reuses the same shapes).
func parseUnifiedDiff(path, raw string) DiffResult {
	empty := func() DiffResult {
		return DiffResult{
			Path:     path,
			Language: guessLanguage(path),
			Hunks:    []DiffHunk{},
		}
	}

	if strings.TrimSpace(raw) == "" {
		return empty()
	}

	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch") {
			result := empty()
			result.Binary = true

			return result
		}
	}

	lines := strings.Split(raw, "\n")
	var hunks []DiffHunk
	var current *DiffHunk
	oldLine, newLine := 0, 0
	additions, deletions := 0, 0
	var oldPath string

	for idx, rawLine := range lines {
		line := strings.TrimSuffix(rawLine, "\r")
		isLast := idx == len(lines)-1

		if strings.HasPrefix(line, "+++ ") {
			continue
		}

		if stripped, ok := strings.CutPrefix(line, "--- "); ok {
			p := strings.TrimSpace(stripped)

			if rest, ok := strings.CutPrefix(p, "a/"); ok {
				oldPath = rest
			} else if p != "/dev/null" {
				oldPath = p
			}

			continue
		}

		if o, ol, n, nl, ok := parseHunkHeader(line); ok {
			if current != nil {
				hunks = append(hunks, *current)
			}

			oldLine, newLine = o, n
			current = &DiffHunk{
				Header:   line,
				OldStart: o,
				OldLines: ol,
				NewStart: n,
				NewLines: nl,
				Lines:    []DiffLine{},
			}
			continue
		}

		// Combined diff (merge) headers use `@@@`; skip without losing the file.
		if strings.HasPrefix(line, "@@@") {
			continue
		}

		if current == nil {
			continue
		}

		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			hunkLine := DiffLine{Type: "add", Content: line[1:], NewLine: intPtr(newLine)}
			newLine++
			current.Lines = append(current.Lines, hunkLine)
			additions++

		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			hunkLine := DiffLine{Type: "del", Content: line[1:], OldLine: intPtr(oldLine)}
			oldLine++
			current.Lines = append(current.Lines, hunkLine)
			deletions++

		case strings.HasPrefix(line, " ") || (line == "" && !isLast):
			content := ""

			if len(line) > 0 {
				content = line[1:]
			}

			current.Lines = append(current.Lines, DiffLine{
				Type:    "context",
				Content: content,
				OldLine: intPtr(oldLine),
				NewLine: intPtr(newLine),
			})
			oldLine++
			newLine++

		case strings.HasPrefix(line, `\`):
			current.Lines = append(current.Lines, DiffLine{Type: "meta", Content: line})
		}
	}

	if current != nil {
		hunks = append(hunks, *current)
	}

	if hunks == nil {
		hunks = []DiffHunk{}
	}

	return DiffResult{
		Path:      path,
		OldPath:   oldPath,
		Language:  guessLanguage(path),
		Hunks:     hunks,
		Additions: additions,
		Deletions: deletions,
	}
}

// parseHunkHeader mirrors `@@ -old[,count] +new[,count] @@...` (counts default 1).
func parseHunkHeader(line string) (int, int, int, int, bool) {
	rest, ok := strings.CutPrefix(line, "@@ ")

	if !ok {
		return 0, 0, 0, 0, false
	}

	ranges, _, found := strings.Cut(rest, " @@")

	if !found {
		return 0, 0, 0, 0, false
	}

	parts := strings.Fields(ranges)

	if len(parts) != 2 {
		return 0, 0, 0, 0, false
	}

	old, ok := strings.CutPrefix(parts[0], "-")

	if !ok {
		return 0, 0, 0, 0, false
	}

	nw, ok := strings.CutPrefix(parts[1], "+")

	if !ok {
		return 0, 0, 0, 0, false
	}

	oStart, oLines, ok1 := parseRange(old)
	nStart, nLines, ok2 := parseRange(nw)

	if !ok1 || !ok2 {
		return 0, 0, 0, 0, false
	}

	return oStart, oLines, nStart, nLines, true
}

func parseRange(s string) (int, int, bool) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	startStr, countStr, hasCount := strings.Cut(s, ",")
	start, err := strconv.Atoi(startStr)

	if err != nil || neg {
		return 0, 0, false
	}

	count := 1

	if hasCount {
		if c, err := strconv.Atoi(countStr); err == nil {
			count = c
		} else {
			return 0, 0, false
		}
	}

	return start, count, true
}

/* --------------------------- commit/compare files -------------------------- */

// CommitFiles mirrors diff.rs fetch_commit_files: `git diff-tree` name-status
// + numstat against the first parent, producing WireWorkingTreeFile rows.
func CommitFiles(ctx context.Context, repoPath, sha string) ([]WireWorkingTreeFile, *GitError) {
	parents := commitParents(repoPath, sha)
	var argsNs, argsNum []string

	if len(parents) > 0 {
		argsNs = []string{"diff-tree", "--no-commit-id", "--name-status", "-r", "-z", parents[0], sha}
		argsNum = []string{"diff", "--numstat", "-z", parents[0], sha}
	} else {
		// Root commit: compare against the empty tree.
		argsNs = []string{"show", "--name-status", "--format=", "-r", "-z", sha}
		argsNum = []string{"show", "--numstat", "--format=", "-r", "-z", sha}
	}

	nsRes, _ := RunGit(ctx, repoPath, argsNs...)
	numRes, _ := RunGit(ctx, repoPath, argsNum...)

	if !nsRes.Ok() {
		return nil, ClassifyGitError(nsRes.Stderr, nsRes.Exit)
	}

	return parseCommitFiles(nsRes.Stdout, numRes.Stdout), nil
}

// parseCommitFiles mirrors diff.rs parse_commit_files.
func parseCommitFiles(nameStatusStdout, numstatStdout string) []WireWorkingTreeFile {
	numstat := parseNumstatZ(numstatStdout)
	files := []WireWorkingTreeFile{}
	id := 0
	tokens := []string{}

	for _, t := range strings.Split(nameStatusStdout, "\x00") {
		if t != "" {
			tokens = append(tokens, t)
		}
	}

	i := 0

	for i < len(tokens) {
		statusToken := tokens[i]
		i++
		status := mapCode(statusToken[0])
		var path, oldPath string

		if status == StatusRenamed || status == StatusCopied {
			if i < len(tokens) {
				oldPath = tokens[i]
				i++
			}

			if i < len(tokens) {
				path = tokens[i]
				i++
			}

			if oldPath == "" {
				oldPath = path
			}
		} else {
			if i < len(tokens) {
				path = tokens[i]
				i++
			}
		}

		if path == "" {
			continue
		}

		fileName, directory := splitFilePath(path)
		files = append(files, WireWorkingTreeFile{
			ID:        fmt.Sprintf("file:%d", id),
			Path:      path,
			FileName:  fileName,
			Directory: directory,
			Status:    status,
			Staged:    false,
			Additions: numstat[path].Additions,
			Deletions: numstat[path].Deletions,
			OldPath:   oldPath,
		})
		id++
	}

	return files
}

// CompareFiles mirrors the compare view: name-status + numstat between two SHAs.
func CompareFiles(ctx context.Context, repoPath, fromSha, toSha string) ([]WireWorkingTreeFile, *GitError) {
	nsRes, _ := RunGit(ctx, repoPath, "diff-tree", "--no-commit-id", "--name-status", "-r", "-z", fromSha, toSha)
	numRes, _ := RunGit(ctx, repoPath, "diff", "--numstat", "-z", fromSha, toSha)

	if !nsRes.Ok() {
		return nil, ClassifyGitError(nsRes.Stderr, nsRes.Exit)
	}

	return parseCommitFiles(nsRes.Stdout, numRes.Stdout), nil
}

/* ------------------------------- language -------------------------------- */

// guessLanguage is a faithful-enough mirror of shared/diff/guessLanguage: map
// extensions to highlight.js language ids.
func guessLanguage(path string) string {
	name := filepath.Base(path)

	switch strings.ToLower(filepath.Ext(name)) {
	case ".ts":
		return "typescript"

	case ".tsx":
		return "typescript"

	case ".js", ".mjs", ".cjs":
		return "javascript"

	case ".jsx":
		return "javascript"

	case ".go":
		return "go"

	case ".rs":
		return "rust"

	case ".py":
		return "python"

	case ".rb":
		return "ruby"

	case ".java":
		return "java"

	case ".c", ".h":
		return "c"

	case ".cpp", ".cc", ".cxx", ".hpp":
		return "cpp"

	case ".swift":
		return "swift"

	case ".kt":
		return "kotlin"

	case ".md", ".markdown":
		return "markdown"

	case ".json":
		return "json"

	case ".yaml", ".yml":
		return "yaml"

	case ".toml":
		return "ini"

	case ".xml":
		return "xml"

	case ".html", ".htm", ".vue":
		return "xml"

	case ".css":
		return "css"

	case ".scss", ".sass":
		return "scss"

	case ".less":
		return "less"

	case ".sh", ".bash", ".zsh":
		return "bash"

	case ".sql":
		return "sql"

	case ".php":
		return "php"

	case ".lua":
		return "lua"

	case ".pl":
		return "perl"

	case ".txt":
		return "plaintext"
	}

	// Dotfiles and lockfiles worth highlighting.
	switch strings.ToLower(name) {
	case "dockerfile":
		return "dockerfile"

	case "makefile":
		return "makefile"

	case "cargo.lock", "package-lock.json":
		return "json"
	}

	return "plaintext"
}
