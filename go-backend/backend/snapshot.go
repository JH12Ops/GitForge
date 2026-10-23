package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

/* Mirror of git.rs snapshot_builder: one WireRepositorySnapshot.

   The wire shapes are consumed directly by the Vue renderer via
   wireSnapshotToRepositoryData. Lane layout is computed client-side, so the
   backend only supplies commits with parents + refs. */

const defaultCommitLimit = 100

// SnapshotOptions mirrors shared/git/models.ts SnapshotOptions.
type SnapshotOptions struct {
	Scopes            []string `json:"scopes,omitempty"`
	CommitLimit       *int     `json:"commitLimit,omitempty"`
	GraphScope        string   `json:"graphScope,omitempty"`
	InvalidateCommits bool     `json:"invalidateCommits,omitempty"`
}

// commitPageRequestWire mirrors models.ts CommitPageRequest with the repo path
// folded in by WailsGitService (the Go backend is stateless per path).
type CommitPageRequestWire struct {
	Path       string `json:"path"`
	BeforeSha  string `json:"beforeSha"`
	Limit      *int   `json:"limit,omitempty"`
	GraphScope string `json:"graphScope,omitempty"`
	Skip       *int   `json:"skip,omitempty"`
}

// CommitPageResultWire mirrors models.ts CommitPageResult.
type CommitPageResultWire struct {
	Commits []WireCommit   `json:"commits"`
	Page    CommitPageInfo `json:"page"`
}

var (
	gitVersionOnce sync.Once
	gitVersionVal  string
)

func cachedGitVersion(ctx context.Context) string {
	gitVersionOnce.Do(func() { gitVersionVal = gitVersion(ctx) })

	return gitVersionVal
}

func scopeEnabled(scopes []string, scope string) bool {
	if len(scopes) == 0 {
		return true
	}

	for _, s := range scopes {
		if s == scope {
			return true
		}
	}

	return false
}

// BuildSnapshot builds a full or scoped snapshot for an already-validated repo.
func BuildSnapshot(ctx context.Context, path string, opts SnapshotOptions) (*WireRepositorySnapshot, *GitError) {
	repo, gerr := buildRepositoryMeta(ctx, path)

	if gerr != nil {
		return nil, gerr
	}

	limit := defaultCommitLimit

	if opts.CommitLimit != nil && *opts.CommitLimit > 0 {
		limit = *opts.CommitLimit
	}

	graphAll := opts.GraphScope != "head" // default 'all'

	snap := &WireRepositorySnapshot{
		Repository:  *repo,
		Branches:    []WireBranch{},
		Tags:        []WireTag{},
		Stashes:     []WireStash{},
		Worktrees:   []WireWorktree{},
		WorkingTree: []WireWorkingTreeFile{},
	}

	commitsWanted := scopeEnabled(opts.Scopes, "commits")
	var authors *AuthorRegistry

	if commitsWanted {
		// Head scope still walks tracked upstreams so remote tips (e.g. a
		// diverged origin/main) decorate inside the focused graph.
		var tracked []string

		if !graphAll {
			tracked = trackedUpstreams(ctx, path)
		}

		commits, total, a, gerr := fetchCommitWindow(ctx, path, limit, graphAll, tracked, 0)

		if gerr != nil {
			return nil, gerr
		}

		authors = a
		snap.Commits = commits
		snap.Page = CommitPageInfo{
			OldestSha: oldestSha(commits),
			HasMore:   len(commits) == limit && total > len(commits),
			Total:     intPtr(total),
		}
	}

	// Branches/tags/stashes also decorate commit refs, so they are loaded
	// alongside commits (cheap parses, and stash bases need the commit map).
	stashes, commitDecorations := fetchStashes(ctx, path, opts.Scopes)

	if scopeEnabled(opts.Scopes, "branches") {
		out, _ := RunGit(ctx, path, branchesCommand()...)
		snap.Branches = parseBranches(out.Stdout, favoritesFor(repo.Path))
	}

	if scopeEnabled(opts.Scopes, "tags") {
		out, _ := RunGit(ctx, path, tagsCommand()...)
		snap.Tags = parseTags(out.Stdout)
	}

	if scopeEnabled(opts.Scopes, "stashes") {
		snap.Stashes = stashes
	}

	if scopeEnabled(opts.Scopes, "worktrees") {
		top := topLevelOrNil(path)
		out, _ := RunGit(ctx, path, "worktree", "list", "--porcelain")

		if top != "" {
			snap.Worktrees = parseWorktrees(out.Stdout, top)
		}
	}

	if scopeEnabled(opts.Scopes, "status") {
		snap.WorkingTree = fetchWorkingTree(ctx, path)
	}

	// Inject stash refs into the loaded commit window (mirror of the stash
	// backfill in log.rs): stash commits decorate even beyond log --all.
	if len(snap.Commits) > 0 && len(stashes) > 0 {
		decorateStashes(ctx, path, stashes, snap.Commits, commitDecorations)
	}

	if !commitsWanted {
		// Scoped snapshot without commits: authors stay empty; the renderer
		// caches the last full list.
		snap.Authors = []WireAuthor{}
	} else if authors != nil {
		snap.Authors = authors.List()
	}

	name, email := gitUserConfig(ctx, path)
	snap.CommitAuthor = WireAuthor{
		Name:     name,
		Email:    email,
		Initials: initialsFor(name),
		Color:    avatarColors[int(hashString(strings.ToLower(email)))%len(avatarColors)],
	}

	return snap, nil
}

// trackedUpstreams returns the short upstream ref of every local branch that
// tracks one (deduped), e.g. origin/main. Used to widen the head-scope walk
// past the current branch so tracked remote tips stay visible.
func trackedUpstreams(ctx context.Context, path string) []string {
	res, _ := RunGit(ctx, path, "for-each-ref", "--format=%(upstream:short)", "refs/heads")
	seen := map[string]bool{}
	var ups []string

	for _, line := range strings.Split(res.Stdout, "\n") {
		up := strings.TrimSpace(line)

		if up == "" || seen[up] {
			continue
		}

		seen[up] = true
		ups = append(ups, up)
	}

	return ups
}

// fetchCommitWindow runs `git log` for the snapshot window; returns commits,
// the total reachable-ref commit count for the same scope, and the author
// registry for those commits. In head scope the tracked upstreams are
// positive tips alongside HEAD, matching gitkraken-style tracking display.
func fetchCommitWindow(ctx context.Context, path string, limit int, graphAll bool, tracked []string, skip int) ([]WireCommit, int, *AuthorRegistry, *GitError) {
	args := []string{"log", "--decorate=short", "--format=" + logFormat(), fmt.Sprintf("-n%d", limit)}

	if skip > 0 {
		args = append(args, fmt.Sprintf("--skip=%d", skip))
	}

	if graphAll {
		args = append(args, "--all")
	} else {
		// HEAD stays implicit unless refs are listed, so base the walk on
		// the current branch explicitly, then add tracked remote tips.
		args = append(args, "HEAD")
		args = append(args, tracked...)
	}

	res, err := RunGit(ctx, path, args...)

	if err != nil || !res.Ok() {
		return nil, 0, nil, ClassifyGitError(res.Stderr, res.Exit)
	}

	authors := NewAuthorRegistry()
	reg := LoadRefRegistry(ctx, path)
	commits := ParseCommits(res.Stdout, authors, reg)

	countArgs := []string{"rev-list", "--count"}

	if graphAll {
		countArgs = append(countArgs, "--all")
	} else {
		countArgs = append(countArgs, "HEAD")

		if len(tracked) > 0 {
			countArgs = append(countArgs, tracked...)
		}
	}

	out, _ := RunGit(ctx, path, countArgs...)
	total := 0

	if n, e := strconv.Atoi(strings.TrimSpace(out.Stdout)); e == nil {
		total = n
	}

	if total < len(commits) {
		total = len(commits)
	}

	return commits, total, authors, nil
}

func oldestSha(commits []WireCommit) *string {
	if len(commits) == 0 {
		return nil
	}

	return strPtr(commits[len(commits)-1].Sha)
}

// fetchStashes mirrors fetch_stashes: parse + enrich each stash with its
// first-parent base. Returns the stash list and a sha→stash-index map used for
// commit decoration.
func fetchStashes(ctx context.Context, path string, scopes []string) ([]WireStash, map[string]int) {
	if !scopeEnabled(scopes, "stashes") && len(scopes) != 0 {
		return nil, nil
	}

	res, _ := RunGit(ctx, path, stashesCommand()...)
	stashes := parseStashes(res.Stdout)
	decorations := map[string]int{}

	for i := range stashes {
		id := stashes[i].ID
		stashes[i].BaseSha, stashes[i].BaseSubject = resolveStashBase(ctx, path, id)
		stashes[i].FilesChanged = countStashFiles(ctx, path, id)

		if shaRes, _ := RunGit(ctx, path, "rev-parse", id); shaRes.Ok() {
			decorations[strings.TrimSpace(shaRes.Stdout)] = stashes[i].Index
		}
	}

	return stashes, decorations
}

// decorateStashes merges stash refs into the commit window. When the stash
// commit itself is not in the window but its base is, the base commit is
// flagged isStashBase (wire contract comment in models.ts).
func decorateStashes(ctx context.Context, path string, stashes []WireStash, commits []WireCommit, decorations map[string]int) {
	_ = decorations
	bySha := map[string]WireCommit{}

	for i := range commits {
		bySha[commits[i].Sha] = commits[i]
	}

	baseFlags := map[string]bool{}

	for i := range stashes {
		shaRes, _ := RunGit(ctx, path, "rev-parse", stashes[i].ID)
		sha := strings.TrimSpace(shaRes.Stdout)

		if sha == "" {
			continue
		}

		if c, ok := bySha[sha]; ok {
			c.Refs = append(c.Refs, WireRef{
				Type:  RefStash,
				Name:  stashes[i].ID,
				Label: "stash",
			})
		} else if stashes[i].BaseSha != nil {
			if _, ok := bySha[*stashes[i].BaseSha]; ok {
				baseFlags[*stashes[i].BaseSha] = true
			}
		}
	}

	if len(baseFlags) > 0 {
		for i := range commits {
			if baseFlags[commits[i].Sha] {
				t := true
				commits[i].IsStashBase = &t
			}
		}
	}
}

// fetchWorkingTree mirrors refs.rs fetch_working_tree (status + both numstats).
func fetchWorkingTree(ctx context.Context, path string) []WireWorkingTreeFile {
	statusOut, _ := RunGit(ctx, path, "status", "--porcelain=v2", "-z", "-uall")
	stagedOut, _ := RunGit(ctx, path, "diff", "--cached", "--numstat", "-z")
	unstagedOut, _ := RunGit(ctx, path, "diff", "--numstat", "-z")

	if !statusOut.Ok() {
		return []WireWorkingTreeFile{}
	}

	return parseStatus(StatusInput{
		Porcelain: statusOut.Stdout,
		Staged:    parseNumstatZ(stagedOut.Stdout),
		Unstaged:  parseNumstatZ(unstagedOut.Stdout),
	})
}

// CommitPage mirrors git.rs commit_page: load the next window of commits.
func CommitPage(ctx context.Context, req CommitPageRequestWire) (*CommitPageResultWire, *GitError) {
	limit := defaultCommitLimit

	if req.Limit != nil && *req.Limit > 0 {
		limit = *req.Limit
	}

	skip := 0

	if req.Skip != nil {
		skip = *req.Skip
	}

	graphAll := req.GraphScope != "head"
	var tracked []string

	if !graphAll {
		tracked = trackedUpstreams(ctx, req.Path)
	}

	commits, total, _, gerr := fetchCommitWindow(ctx, req.Path, limit, graphAll, tracked, skip)

	if gerr != nil {
		return nil, gerr
	}

	return &CommitPageResultWire{
		Commits: commits,
		Page: CommitPageInfo{
			OldestSha: oldestSha(commits),
			HasMore:   len(commits) == limit && total > skip+len(commits),
			Total:     intPtr(total),
		},
	}, nil
}

/* --------------------------- repository metadata --------------------------- */

func topLevelOrNil(path string) string {
	res, _ := RunGit(context.Background(), path, "rev-parse", "--show-toplevel")

	return strings.TrimSpace(res.Stdout)
}

func gitDirOrNil(path string) string {
	res, _ := RunGit(context.Background(), path, "rev-parse", "--absolute-git-dir")

	return strings.TrimSpace(res.Stdout)
}

func gitUserConfig(ctx context.Context, path string) (string, string) {
	nameRes, _ := RunGit(ctx, path, "config", "user.name")
	emailRes, _ := RunGit(ctx, path, "config", "user.email")

	return strings.TrimSpace(nameRes.Stdout), strings.TrimSpace(emailRes.Stdout)
}

// buildRepositoryMeta is the metadata half of build_snapshot in git.rs.
func buildRepositoryMeta(ctx context.Context, path string) (*WireRepository, *GitError) {
	abs, err := filepath.Abs(path)

	if err != nil {
		abs = path
	}

	info, err := os.Stat(abs)

	if err != nil {
		return nil, gitErr(ErrCodeRepositoryNotFound, fmt.Sprintf("%s does not exist", abs), nil)
	}

	if !info.IsDir() {
		return nil, gitErr(ErrCodeRepositoryNotFound, fmt.Sprintf("%s is not a directory", abs), nil)
	}

	// Validity: must be inside a work tree (bare repos are unsupported).
	bareRes, sperr := RunGit(ctx, abs, "rev-parse", "--is-bare-repository")

	if sperr != nil || !bareRes.Ok() {
		return nil, ClassifyGitError(bareRes.Stderr, bareRes.Exit)
	}

	if strings.TrimSpace(bareRes.Stdout) == "true" {
		return nil, gitErr(ErrCodeBareRepository, "", nil)
	}

	name := filepath.Base(abs)

	if name == "" || name == "." || name == "/" {
		name = abs
	}

	currentBranch := ""
	branchRes, _ := RunGit(ctx, abs, "branch", "--show-current")
	currentBranch = strings.TrimSpace(branchRes.Stdout)
	detached := currentBranch == ""

	headRes, _ := RunGit(ctx, abs, "rev-parse", "HEAD")
	headSha := strings.TrimSpace(headRes.Stdout)

	ahead, behind := 0, 0

	if currentBranch != "" {
		upRes, _ := RunGit(ctx, abs, "for-each-ref",
			"--format="+"%(upstream:short)",
			"refs/heads/"+currentBranch)
		upstream := strings.TrimSpace(upRes.Stdout)

		if upstream != "" {
			abRes, _ := RunGit(ctx, abs, "rev-list", "--left-right", "--count", upstream+"...HEAD")
			fields := strings.Fields(abRes.Stdout)

			if len(fields) == 2 {
				if n, err := strconv.Atoi(fields[0]); err == nil {
					behind = n // left = upstream-only commits
				}

				if n, err := strconv.Atoi(fields[1]); err == nil {
					ahead = n // right = local-only commits
				}
			}
		}
	}

	remotesRes, _ := RunGit(ctx, abs, "remote", "-v")
	remotes := parseRemotes(remotesRes.Stdout)

	if remotes == nil {
		remotes = []WireRemote{} // JSON arrays, never null
	}

	remoteURL := ""

	for _, r := range remotes {
		if r.FetchURL != "" {
			remoteURL = r.FetchURL
			break
		}
	}

	repo := &WireRepository{
		Name:          name,
		Path:          abs,
		GitVersion:    cachedGitVersion(ctx),
		CurrentBranch: currentBranch,
		Detached:      detached,
		HeadSha:       headSha,
		Ahead:         ahead,
		Behind:        behind,
		RemoteURL:     remoteURL,
		Remotes:       remotes,
		State:         detectRepoState(abs, gitDirOrNil(abs), detached),
	}
	applyJournalState(repo)

	return repo, nil
}

// applyJournalState patches the undo/redo affordances from the repository's
// journal (mirror of main.rs::apply_journal_state).
func applyJournalState(repo *WireRepository) {
	wire := journalFor(repo.Path).State(repo.State)
	repo.CanUndo = wire.CanUndo
	repo.CanRedo = wire.CanRedo
	repo.UndoLabel = wire.UndoLabel
	repo.RedoLabel = wire.RedoLabel
}

// detectRepoState inspects the .git directory for in-progress operations
// (mirror of the RepoState detection in git.rs).
func detectRepoState(worktree, gitDir string, detached bool) RepoState {
	state := RepoState{DetachedHead: detached, OperationLabel: nil}

	if gitDir == "" {
		return state
	}

	exists := func(parts ...string) bool {
		_, err := os.Stat(filepath.Join(append([]string{gitDir}, parts...)...))

		return err == nil
	}

	label := ""

	switch {
	case exists("MERGE_HEAD"):
		state.Merging = true
		label = "Merging"

	case exists("rebase-merge"):
		state.Rebasing = true
		label = "Rebasing"

		if n, total := readRebaseCounts(gitDir); n > 0 {
			label = fmt.Sprintf("Rebasing (%d/%d)", n, total)
		}

	case exists("rebase-apply"):
		state.Rebasing = true
		label = "Rebasing"

	case exists("CHERRY_PICK_HEAD"):
		state.CherryPicking = true
		label = "Cherry-picking"

	case exists("REVERT_HEAD"):
		state.Reverting = true
		label = "Reverting"

	case exists("BISECT_LOG"):
		state.Bisecting = true
		label = "Bisecting"
	}

	if label != "" {
		state.OperationLabel = strPtr(label)
	}

	return state
}

func readRebaseCounts(gitDir string) (int, int) {
	num := readIntFile(filepath.Join(gitDir, "rebase-merge", "msgnum"))
	total := readIntFile(filepath.Join(gitDir, "rebase-merge", "end"))

	return num, total
}

func readIntFile(path string) int {
	data, err := os.ReadFile(path)

	if err != nil {
		return 0
	}

	n, err := strconv.Atoi(strings.TrimSpace(string(data)))

	if err != nil {
		return 0
	}

	return n
}
