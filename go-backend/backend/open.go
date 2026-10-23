package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

/* Mirror of the open/onboarding flows in git.rs + repos.rs. */

// ValidateRepo mirrors the activate/open validation in Rust: exists, is a
// Git worktree, is not bare. Returns the canonicalized repository path.
func ValidateRepo(ctx context.Context, path string) (string, *GitError) {
	abs, err := filepath.Abs(path)

	if err != nil {
		abs = path
	}

	if fi, serr := os.Stat(abs); serr != nil || !fi.IsDir() {
		return "", gitErr(ErrCodeRepositoryNotFound, fmt.Sprintf("%s does not exist", abs), nil)
	}

	inTree, _ := RunGit(ctx, abs, "rev-parse", "--is-inside-work-tree")

	if !inTree.Ok() {
		return "", ClassifyGitError(inTree.Stderr, inTree.Exit)
	}

	bare, _ := RunGit(ctx, abs, "rev-parse", "--is-bare-repository")

	if strings.TrimSpace(bare.Stdout) == "true" {
		return "", gitErr(ErrCodeBareRepository, "", nil)
	}

	return abs, nil
}

// OpenRepo = validate + full snapshot (mirror of the `git_open_repo` command).
// opts carries the renderer's snapshot options (graph scope, commit limit).
func OpenRepo(ctx context.Context, path string, opts SnapshotOptions) (*OpenRepoResult, *GitError) {
	abs, gerr := ValidateRepo(ctx, path)

	if gerr != nil {
		return nil, gerr
	}

	snap, gerr := BuildSnapshot(ctx, abs, opts)

	if gerr != nil {
		return nil, gerr
	}

	return &OpenRepoResult{OK: true, Snapshot: snap}, nil
}

/* ----------------------------- onboarding ---------------------------------- */

// CloneRepo mirrors the `git_clone_repo` command in onboarding.rs semantics:
// clone into <parentPath>/<name> (name defaulted from the URL), then snapshot.
func CloneRepo(ctx context.Context, req CloneRepoRequestWire) (*OpenRepoResult, *GitError) {
	url := strings.TrimSpace(req.URL)
	parent := strings.TrimSpace(req.ParentPath)

	if url == "" || parent == "" {
		return nil, gitErr(ErrCodeUnknown, "clone requires a URL and a parent folder", nil)
	}

	name := strings.TrimSpace(req.Name)

	if name == "" {
		name = repoNameFromURL(url)
	}

	dest := filepath.Join(parent, name)

	if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
		return nil, gitErr(ErrCodeUnknown, fmt.Sprintf("%s already exists", dest), nil)
	}

	if _, serr := os.Stat(parent); serr != nil {
		return nil, gitErr(ErrCodeRepositoryNotFound, fmt.Sprintf("%s does not exist", parent), nil)
	}

	res, err := RunGit(ctx, parent, "clone", "--origin", "origin", url, dest)

	if err != nil {
		return nil, unknownErr("git clone could not be started: " + err.Error())
	}

	if !res.Ok() {
		return nil, ClassifyGitError(res.Stderr, res.Exit)
	}

	return OpenRepo(ctx, dest, req.Options)
}

func repoNameFromURL(url string) string {
	s := strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(url), "/"), ".git")

	if idx := strings.LastIndexAny(s, "/:"); idx != -1 {
		s = s[idx+1:]
	}

	return s
}

// InitRepo mirrors the `git_init_repo` command: `git init [-b branch]` + snapshot.
func InitRepo(ctx context.Context, req InitRepoRequestWire) (*OpenRepoResult, *GitError) {
	path := strings.TrimSpace(req.Path)

	if path == "" {
		return nil, gitErr(ErrCodeUnknown, "init requires a path", nil)
	}

	args := []string{"init"}

	if req.DefaultBranch != "" {
		args = append(args, "-b", req.DefaultBranch)
	}

	args = append(args, path)
	res, err := RunGit(ctx, "", args...)

	if err != nil {
		return nil, unknownErr("git init could not be started: " + err.Error())
	}

	if !res.Ok() {
		return nil, ClassifyGitError(res.Stderr, res.Exit)
	}

	return OpenRepo(ctx, path, req.Options)
}

// DiscoverRepos mirrors repos.rs discover_repos: walk root (depth-limited,
// skipping heavy/ignored dirs) collecting directories that contain a
// `.git` entry.
func DiscoverRepos(ctx context.Context, req DiscoverReposRequestWire) []DiscoveredRepo {
	root := strings.TrimSpace(req.Root)

	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return []DiscoveredRepo{}
	}

	maxDepth := req.MaxDepth

	if maxDepth <= 0 {
		maxDepth = 3
	}

	var out []DiscoveredRepo
	skip := map[string]bool{"node_modules": true, ".git": true, "target": true,
		"dist": true, "build": true, ".cache": true, "venv": true, ".venv": true,
		"vendor": true, "__pycache__": true, "Pods": true, ".tox": true}
	walk(root, root, 0, maxDepth, skip, &out, map[string]bool{})

	return out
}

func walk(base, dir string, depth, maxDepth int, skip map[string]bool, out *[]DiscoveredRepo, visited map[string]bool) {
	if depth > maxDepth || len(*out) >= 100 {
		return
	}

	entries, err := os.ReadDir(dir)

	if err != nil {
		return
	}

	resolved, _ := filepath.EvalSymlinks(dir)

	if visited[resolved] {
		return
	}

	visited[resolved] = true

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		name := e.Name()

		if strings.HasPrefix(name, ".DS_Store") {
			continue
		}

		if name == ".git" {
			rep := filepath.Dir(filepath.Join(dir, name))

			if !visited[resolved] && !visited[rep] {
				visited[rep] = true
				*out = append(*out, DiscoveredRepo{Path: rep, Name: filepath.Base(rep)})
			}

			continue
		}

		if skip[name] {
			continue
		}

		walk(base, filepath.Join(dir, name), depth+1, maxDepth, skip, out, visited)
	}
}

/* -------------------------------- OS glue ---------------------------------- */

// OpenTerminalExternal mirrors the `git_open_terminal_external` command.
func OpenTerminalExternal(path string) (bool, string) {
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return false, "path is not a directory"
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-a", "Terminal", path)

	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)

	default:
		term := os.Getenv("GITFORGE_TERMINAL")

		if term == "" {
			term = "x-terminal-emulator"
		}

		cmd = exec.Command(term)
		cmd.Dir = path
	}

	if err := cmd.Start(); err != nil {
		return false, err.Error()
	}

	return true, ""
}

// RevealPath mirrors the `git_reveal_path` command (finder/explorer show-in).
func RevealPath(path string) (bool, string) {
	if _, err := os.Stat(path); err != nil {
		return false, "path does not exist"
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", path)

	case "windows":
		cmd = exec.Command("explorer", "/select,", path)

	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}

	if err := cmd.Start(); err != nil {
		return false, err.Error()
	}

	return true, ""
}

/* --------------------------------- search ---------------------------------- */

type SearchRequestWire struct {
	Path       string   `json:"path"`
	Text       string   `json:"text"`
	Fields     []string `json:"fields,omitempty"`
	Regex      bool     `json:"regex,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	GraphScope string   `json:"graphScope,omitempty"`
}

// Search mirrors the search slice of main.rs: commit hits by message/author/
// sha plus file matches from `git grep -l`.
func Search(ctx context.Context, req SearchRequestWire) (*SearchResults, *GitError) {
	text := strings.TrimSpace(req.Text)
	results := &SearchResults{Commits: []SearchCommitHit{}, Files: []string{}}

	if text == "" {
		return results, nil
	}

	limit := req.Limit

	if limit <= 0 {
		limit = 50
	}

	// Build --grep flags for selected fields (default: message).
	fields := req.Fields

	if len(fields) == 0 {
		fields = []string{"message"}
	}

	hasAuthor, hasMessage, hasSHA := false, false, false

	for _, f := range fields {
		switch f {
		case "author":
			hasAuthor = true

		case "message":
			hasMessage = true

		case "sha":
			hasSHA = true
		}
	}

	if hasSHA && len(text) >= 4 {
		// Prefix search over loaded refs is enough for sha hits.
		res, _ := RunGit(ctx, req.Path, "rev-parse", "--verify", text+"^{commit}")

		if sha := strings.TrimSpace(res.Stdout); sha != "" && !strings.ContainsAny(text, "*?[]") {
			results.Commits = append(results.Commits, hitFromLog(ctx, req.Path, sha))
		}
	}

	args := []string{"log", fmt.Sprintf("-n%d", limit), "--format=" + logFormat(), "--all-match"}
	regexMode := "--fixed-strings"

	if req.Regex {
		regexMode = "--extended-regexp"
	}

	args = append(args, regexMode)

	if hasMessage {
		args = append(args, "--grep="+text)
	}

	if graphAll(req.GraphScope) {
		args = append(args, "--all")
	}

	if hasAuthor {
		args = append(args, "--author="+text)
	}

	res, _ := RunGit(ctx, req.Path, args...)

	if res.Ok() {
		authors := NewAuthorRegistry()
		commits := ParseCommits(res.Stdout, authors, LoadRefRegistry(ctx, req.Path))

		for _, c := range commits {
			if len(results.Commits) >= limit {
				break
			}

			results.Commits = append(results.Commits, SearchCommitHit{
				Sha:      c.Sha,
				ShortSha: c.ShortSha,
				Subject:  c.Subject,
				Author:   c.Author.Name,
				Date:     c.Date,
			})
		}
	}

	// File matches (best effort; optional on failure).
	grepArgs := []string{"grep", "-l", regexMode, "--", text}
	gres, _ := RunGit(ctx, req.Path, grepArgs...)

	if gres.Ok() {
		for _, line := range strings.Split(gres.Stdout, "\n") {
			line = strings.TrimSpace(line)

			if line != "" && len(results.Files) < limit {
				results.Files = append(results.Files, line)
			}
		}
	}

	return results, nil
}

func hitFromLog(ctx context.Context, path, sha string) SearchCommitHit {
	res, _ := RunGit(ctx, path, "log", "-1", "--format="+logFormat(), sha)
	authors := NewAuthorRegistry()
	commits := ParseCommits(res.Stdout, authors, nil)

	if len(commits) > 0 {
		c := commits[0]

		return SearchCommitHit{Sha: c.Sha, ShortSha: c.ShortSha, Subject: c.Subject, Author: c.Author.Name, Date: c.Date}
	}

	return SearchCommitHit{Sha: sha}
}

func graphAll(scope string) bool { return scope != "head" }

/* -------------------------- wire request wrappers -------------------------- */

type CloneRepoRequestWire struct {
	URL        string          `json:"url"`
	ParentPath string          `json:"parentPath"`
	Name       string          `json:"name,omitempty"`
	Options    SnapshotOptions `json:"options,omitempty"`
}

type InitRepoRequestWire struct {
	Path          string          `json:"path"`
	DefaultBranch string          `json:"defaultBranch,omitempty"`
	Options       SnapshotOptions `json:"options,omitempty"`
}

type DiscoverReposRequestWire struct {
	Root     string `json:"root"`
	MaxDepth int    `json:"maxDepth,omitempty"`
}

type DiscoveredRepo struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// jsonOf = debug helper.
var _ = json.Marshal
