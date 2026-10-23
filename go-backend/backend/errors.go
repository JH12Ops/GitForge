package backend

import (
	"fmt"
	"strings"
)

// GitError is the typed git error taxonomy (mirror of shared/git/errors.ts).
type GitError struct {
	Code    string
	Message string
	Detail  string
	Files   []string
}

func (e *GitError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.Detail)
	}

	return e.Message
}

func (e *GitError) Serialize() *SerializedGitError {
	ser := e.serializeValue()

	return &ser
}

func (e *GitError) serializeValue() SerializedGitError {
	return SerializedGitError{
		GitError: true,
		Code:     e.Code,
		Message:  e.Message,
		Detail:   e.Detail,
		Files:    e.Files,
	}
}

var defaultMessages = map[string]string{
	ErrCodeMergeConflict:      "The operation produced merge conflicts that need to be resolved.",
	ErrCodeAuthentication:     "Authentication failed. Check your SSH keys or credential manager.",
	ErrCodeRepositoryNotFound: "The repository could not be found.",
	ErrCodeNotARepository:     "This folder is not a Git repository.",
	ErrCodeBareRepository:     "Bare repositories are not supported.",
	ErrCodeDetachedHead:       "HEAD is detached.",
	ErrCodeRemoteRejected:     "The remote rejected the push.",
	ErrCodeNoUpstream:         "This branch has no upstream on the remote yet.",
	ErrCodeNothingToCommit:    "There are no staged changes to commit.",
	ErrCodeNetwork:            "A network error occurred while contacting the remote.",
	ErrCodeDirtyWorktree:      "You have uncommitted changes that would be overwritten.",
	ErrCodeCancelled:          "The operation was cancelled.",
	ErrCodeUnknown:            "An unexpected Git error occurred.",
}

func gitErr(code, detail string, files []string) *GitError {
	msg, ok := defaultMessages[code]

	if !ok {
		msg = code
	}

	return &GitError{Code: code, Message: msg, Detail: firstLine(detail), Files: files}
}

func unknownErr(message string) *GitError {
	return &GitError{Code: ErrCodeUnknown, Message: message}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)

	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)

		if line != "" {
			return line
		}
	}

	return ""
}

// ClassifyGitError mirrors shared/git/errors.ts classifyGitError: pattern-match
// git stderr into an actionable taxonomy instead of dumping raw stderr.
func ClassifyGitError(stderr string, exit int) *GitError {
	text := strings.TrimSpace(stderr)
	lower := strings.ToLower(text)
	align := firstLine(text)

	contains := func(needles ...string) bool {
		for _, n := range needles {
			if strings.Contains(lower, n) {
				return true
			}
		}

		return false
	}

	switch {
	case contains(
		"could not read username",
		"authentication failed",
		"permission denied (publickey)",
		"host key verification failed",
	):
		return gitErr(ErrCodeAuthentication, align, nil)

	case contains("conflict") &&
		(contains("merge", "automatic", "cherry-pick", "rebase")):
		files := extractConflictFiles(text)

		if len(files) == 0 {
			files = nil
		}

		return gitErr(ErrCodeMergeConflict, align, files)

	case contains("nothing to commit", "no changes added to commit"):
		return gitErr(ErrCodeNothingToCommit, align, nil)

	case contains("rejected") && contains("non-fast-forward"):
		return gitErr(ErrCodeRemoteRejected, align, nil)

	case contains("failed to push"):
		return gitErr(ErrCodeRemoteRejected, align, nil)

	case contains("has no upstream branch", "no tracking information"):
		return gitErr(ErrCodeNoUpstream, align, nil)

	case contains("could not resolve host", "connection timed out", "network is unreachable"):
		return gitErr(ErrCodeNetwork, align, nil)

	case contains("would be overwritten", "your local changes", "please commit your changes or stash"):
		return gitErr(ErrCodeDirtyWorktree, align, nil)

	case contains("not a git repository"):
		return gitErr(ErrCodeNotARepository, align, nil)
	}

	return &GitError{
		Code:    ErrCodeUnknown,
		Message: align,
		Detail:  fmt.Sprintf("git exited with code %d", exit),
	}
}

func extractConflictFiles(text string) []string {
	seen := map[string]bool{}
	var files []string

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		var file string

		if strings.HasPrefix(line, "CONFLICT") && strings.Contains(line, "Merge conflict in ") {
			file = strings.TrimSpace(line[strings.Index(line, "Merge conflict in ")+len("Merge conflict in "):])
		} else if strings.HasPrefix(line, "Auto-merging ") {
			file = strings.TrimSpace(strings.TrimPrefix(line, "Auto-merging "))
		}

		if file != "" && !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}

	return files
}
