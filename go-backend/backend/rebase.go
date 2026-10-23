package backend

import (
	"context"
	"strconv"
	"strings"
	"time"
)

/* Rebase-plan read endpoint (port of diff.rs fetch_rebase_commits): the
commits between an upstream base and HEAD, rendered for the interactive
rebase todo editor. */

// rebaseLogFormat mirrors REBASE_LOG_FORMAT: unit separators between fields,
// record separator between commits.
const rebaseLogFormat = "%H\x1f%h\x1f%s\x1f%an\x1f%at\x1e"

type RebaseCommitEntry struct {
	ShortSha   string `json:"shortSha"`
	Subject    string `json:"subject"`
	AuthorName string `json:"authorName"`
	Sha        string `json:"sha"`
	Date       string `json:"date"`
}

type RebaseCommitsResult struct {
	Base         string              `json:"base"`
	BaseShortSha string              `json:"baseShortSha"`
	Commits      []RebaseCommitEntry `json:"commits"`
}

// parseRebaseCommitLog mirrors parse_rebase_commits over the \x1e/\x1f
// delimited log records.
func parseRebaseCommitLog(stdout string) []RebaseCommitEntry {
	entries := []RebaseCommitEntry{}

	for _, record := range strings.Split(stdout, "\x1e") {
		record = strings.TrimSpace(record)

		if record == "" {
			continue
		}

		fields := strings.SplitN(record, "\x1f", 5)

		if len(fields) < 5 {
			continue
		}

		fields[4] = strings.TrimSuffix(fields[4], "\x1e")
		sha := strings.TrimSpace(fields[0])
		shortSha := strings.TrimSpace(fields[1])
		subject := strings.TrimSpace(fields[2])

		if sha == "" || subject == "" {
			continue
		}

		date := epochISO

		if unix, uerr := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64); uerr == nil {
			date = formatISO(time.Unix(unix, 0))
		} else {
			date = parseGitDate(fields[4])
		}

		entries = append(entries, RebaseCommitEntry{
			Sha:        sha,
			ShortSha:   shortSha,
			Subject:    subject,
			AuthorName: fields[3],
			Date:       date,
		})
	}

	return entries
}

// FetchRebaseCommits mirrors fetch_rebase_commits: validate the base, take
// its short sha, and list base..HEAD oldest-first.
func FetchRebaseCommits(ctx context.Context, repoPath string, base string) (*RebaseCommitsResult, *GitError) {
	base = strings.TrimSpace(base)

	if base == "" {
		return nil, unknownErr("No base ref specified.")
	}

	shortRes, err := RunGit(ctx, repoPath, "rev-parse", "--short", base)

	if err != nil {
		return nil, unknownErr("failed to spawn git")
	}

	if shortRes.Exit != 0 {
		return nil, unknownErr(`Could not resolve base ref "` + base + `".`)
	}

	logRes, err := RunGit(ctx, repoPath, "log", "--reverse", "--format="+rebaseLogFormat, base+"..HEAD")

	if err != nil {
		return nil, unknownErr("failed to spawn git")
	}

	commits := parseRebaseCommitLog(logRes.Stdout)

	if len(commits) == 0 {
		return nil, unknownErr("No commits between " + base + " and HEAD.")
	}

	return &RebaseCommitsResult{
		Base:         base,
		BaseShortSha: strings.TrimSpace(shortRes.Stdout),
		Commits:      commits,
	}, nil
}
