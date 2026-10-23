package backend

import (
	"context"
	"strconv"
	"strings"
)

/* Mirror of diff.rs fetch_blame + fetch_file_history. */

// WireBlameLine mirrors shared/git/models.ts WireBlameLine.
type WireBlameLine struct {
	LineNumber  int    `json:"lineNumber"`
	ShortSha    string `json:"shortSha"`
	CommitSha   string `json:"commitSha"`
	Author      string `json:"author"`
	AuthorEmail string `json:"authorEmail"`
	AuthorTime  int64  `json:"authorTime"`
	Summary     string `json:"summary"`
	Content     string `json:"content"`
}

type BlameResult struct {
	Path     string          `json:"path"`
	Language string          `json:"language"`
	Lines    []WireBlameLine `json:"lines"`
}

// FetchBlame mirrors fetch_blame: the source selects the blamed revision.
func FetchBlame(ctx context.Context, repoPath string, req DiffRequestWire) (*BlameResult, *GitError) {
	safe, gerr := assertPathInsideRepo(repoPath, req.Path)

	if gerr != nil {
		return nil, gerr
	}

	var args []string

	switch req.Source {
	case "index":
		args = []string{"blame", "--line-porcelain", "--cached"}

	case "commit":
		args = []string{"blame", "--line-porcelain", deref(req.Sha, "HEAD")}

	case "range":
		args = []string{"blame", "--line-porcelain", deref(req.ToSha, deref(req.Sha, "HEAD"))}

	default: // worktree
		args = []string{"blame", "--line-porcelain"}
	}

	args = append(args, "--", safe)

	res, err := RunGit(ctx, repoPath, args...)

	if err != nil || !res.Ok() {
		return nil, ClassifyGitError(res.Stderr, res.Exit)
	}

	return &BlameResult{
		Path:     safe,
		Language: guessLanguage(safe),
		Lines:    parseBlamePorcelain(res.Stdout),
	}, nil
}

// parseBlamePorcelain mirrors diff.rs parse_blame_porcelain (line-porcelain:
// every line repeats its header; metadata lines belong to the current header).
func parseBlamePorcelain(stdout string) []WireBlameLine {
	lines := []WireBlameLine{}
	raw := strings.Split(stdout, "\n")
	i := 0

	for i < len(raw) {
		header := raw[i]

		if header == "" {
			i++
			continue
		}

		sha, lineNumber, ok := parseBlameHeader(header)

		if !ok {
			i++
			continue
		}

		i++
		entry := WireBlameLine{LineNumber: lineNumber, CommitSha: sha, ShortSha: truncateSha(sha, 7)}

		for i < len(raw) {
			line := raw[i]

			if rest, ok := strings.CutPrefix(line, "\t"); ok {
				entry.Content = rest
				i++
				break
			}

			if rest, ok := strings.CutPrefix(line, "author "); ok {
				entry.Author = rest
			} else if rest, ok := strings.CutPrefix(line, "author-mail "); ok {
				entry.AuthorEmail = strings.Trim(strings.TrimSpace(rest), "<>")
			} else if rest, ok := strings.CutPrefix(line, "author-time "); ok {
				if t, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64); err == nil {
					entry.AuthorTime = t // unix seconds (wire: number)
				}
			} else if rest, ok := strings.CutPrefix(line, "summary "); ok {
				entry.Summary = rest
			}

			i++
		}

		lines = append(lines, entry)
	}

	return lines
}

func truncateSha(sha string, n int) string {
	if len(sha) > n {
		return sha[:n]
	}

	return sha
}

// parseBlameHeader mirrors `<sha> <orig> <final>` with optional boundary `^`.
func parseBlameHeader(header string) (string, int, bool) {
	header = strings.TrimPrefix(header, "^")
	parts := strings.Fields(header)

	if len(parts) < 3 {
		return "", 0, false
	}

	sha := parts[0]

	if !(4 <= len(sha) && len(sha) <= 40) || !isHexSha(sha) {
		return "", 0, false
	}

	n, err := strconv.Atoi(parts[2])

	if err != nil {
		return "", 0, false
	}

	return sha, n, true
}

func isHexSha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]

		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}

	return true
}

/* ------------------------------ file history ------------------------------- */

type FileHistoryEntry struct {
	Sha      string `json:"sha"`
	ShortSha string `json:"shortSha"`
	Subject  string `json:"subject"`
	Author   string `json:"author"`
	Date     string `json:"date"`
}

type FileHistoryResult struct {
	Path    string             `json:"path"`
	Entries []FileHistoryEntry `json:"entries"`
}

const fileHistoryFormat = "%H\x1f%s\x1f%an\x1f%at"

// FetchFileHistory mirrors fetch_file_history (follows renames by default).
func FetchFileHistory(ctx context.Context, repoPath string, file string, limit int) (*FileHistoryResult, *GitError) {
	safe, gerr := assertPathInsideRepo(repoPath, file)

	if gerr != nil {
		return nil, gerr
	}

	if limit <= 0 {
		limit = 200
	}

	res, err := RunGit(ctx, repoPath, "log", "--follow",
		"--format="+fileHistoryFormat, "-n", strconv.Itoa(limit), "--", safe)

	if err != nil || !res.Ok() {
		return nil, ClassifyGitError(res.Stderr, res.Exit)
	}

	entries := []FileHistoryEntry{}

	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSuffix(line, "\r")

		if line == "" {
			continue
		}

		fields := strings.Split(line, "\x1f")

		if len(fields) < 4 {
			continue
		}

		entries = append(entries, FileHistoryEntry{
			Sha:      fields[0],
			ShortSha: truncateSha(fields[0], 7),
			Subject:  fields[1],
			Author:   fields[2],
			Date:     parseGitDate(fields[3]),
		})
	}

	return &FileHistoryResult{Path: safe, Entries: entries}, nil
}
