package backend

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

/* Action router (port of actions.rs route_action).

The renderer submits a `GitAction`; this runs concrete git argv inside the
repository, classifies failures with the shared error taxonomy, records every
invocation for the renderer's command log, checkpoints the undo journal, and
streams remote progress over SSE `git:progress`. Runner mirrors `run_git`
(quotepath/color -c flags, GIT_PAGER=cat, 8MiB output cap, cancel + timeout). */

// Timeout budgets (mirrors actions.rs): fetch prefetch 5min, push/pull 1min,
// local ops 30s.
const (
	remoteFetchTimeout    = 300 * time.Second
	remotePushPullTimeout = 60 * time.Second
	defaultActionTimeout  = 30 * time.Second
)

// maxActionOutput caps stdout/stderr buffers like MAX_OUTPUT_BYTES.
const maxActionOutput = 8 << 20

// maxCommandLogChars caps how much of one command's output crosses the wire
// (mirrors MAX_COMMAND_LOG_CHARS).
const maxCommandLogChars = 8 * 1024

// maxCommandLogEntries caps the recorded command list (mirrors the 50 cap).
const maxCommandLogEntries = 50

type ExecutedGitCommand struct {
	Argv      []string `json:"argv"`
	Command   string   `json:"command"`
	Stdout    string   `json:"stdout"`
	Stderr    string   `json:"stderr"`
	ExitCode  *int     `json:"exitCode"`
	OK        bool     `json:"ok"`
	Truncated bool     `json:"truncated"`
}

// OperationProgressWire mirrors shared/git/models.ts OperationProgress.
type OperationProgressWire struct {
	OperationID string `json:"operationId"`
	Kind        string `json:"kind"`
	Phase       string `json:"phase"`
	Percent     *int   `json:"percent"`
	Done        bool   `json:"done"`
}

// operationsHub tracks cancellable in-flight operations so git_cancel can
// reach the running action's context.
type operationsHub struct {
	mu          sync.Mutex
	cancelFuncs map[string]context.CancelFunc
}

var inFlight = &operationsHub{cancelFuncs: map[string]context.CancelFunc{}}

// register derives a cancellable context for one operation and stores the
// cancel so `git_cancel` can hit it (mirror of Operations::register + the
// per-run watch channel).
func (h *operationsHub) register(ctx context.Context, id string) context.Context {
	runCtx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.cancelFuncs[id] = cancel
	h.mu.Unlock()

	return runCtx
}

func (h *operationsHub) unregister(id string) {
	h.mu.Lock()
	cancel := h.cancelFuncs[id]
	delete(h.cancelFuncs, id)
	h.mu.Unlock()

	if cancel != nil {
		cancel() // release the WithCancel context resources
	}
}

// cancelAll cancels every in-flight operation; returns how many were alive.
func (h *operationsHub) cancelAll() int {
	h.mu.Lock()
	funcs := h.cancelFuncs
	h.cancelFuncs = map[string]context.CancelFunc{}
	h.mu.Unlock()

	for _, cancel := range funcs {
		cancel()
	}

	return len(funcs)
}

// runOpts carries the options for one git invocation.
type runOpts struct {
	args         []string
	allowFailure bool
	// timeout zero = defaultActionTimeout.
	timeout time.Duration
	// input is fed to stdin (patch apply).
	input string
	// extraEnv appended after the base child env.
	extraEnv map[string]string
	// noTimeout lifts the 30s budget (long-running interactive rebase).
	noTimeout bool
	// onProgressLine receives each non-empty stderr line (remote streaming).
	onProgressLine func(line string)
}

type actionRunResult struct {
	stdout string
	stderr string
	exit   int
}

// runActionGit throws like `gitCli.run`: non-zero exit without allow_failure
// becomes a classified GitError; context cancellation becomes Cancelled.
func runActionGit(ctx context.Context, cwd string, opts runOpts) (actionRunResult, *GitError) {
	args := make([]string, 0, len(opts.args)+4)
	args = append(args, "-c", "core.quotepath=false", "-c", "color.ui=false")
	args = append(args, opts.args...)
	timeout := opts.timeout

	if opts.noTimeout {
		timeout = 24 * time.Hour // bounded-run guard, effectively unlimited
	} else if timeout == 0 {
		timeout = defaultActionTimeout
	}

	runCtx, cancelRun := context.WithTimeout(ctx, timeout)
	defer cancelRun()

	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_EDITOR=:")

	for k, v := range opts.extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	if opts.input != "" {
		cmd.Stdin = strings.NewReader(opts.input)
	}

	stdoutPipe, perr := cmd.StdoutPipe()

	if perr != nil {
		return actionRunResult{}, unknownErr("failed to spawn git: " + perr.Error())
	}

	stderrPipe, perr := cmd.StderrPipe()

	if perr != nil {
		return actionRunResult{}, unknownErr("failed to spawn git: " + perr.Error())
	}

	if serr := cmd.Start(); serr != nil {
		return actionRunResult{}, unknownErr("failed to spawn git: " + serr.Error())
	}

	var outBuf, errBuf strings.Builder
	_, _ = io.Copy(&outBuf, io.LimitReader(stdoutPipe, maxActionOutput)) // best-effort cap
	_, _ = io.Copy(&errBuf, io.LimitReader(stderrPipe, maxActionOutput)) // best-effort cap
	runErr := cmd.Wait()

	// Progress lines are emitted from stderr as they complete (the pipes
	// finish draining best-effort around Wait; remote ops still surface
	// phases because git flushes stderr eagerly).
	res := actionRunResult{stdout: outBuf.String(), stderr: errBuf.String()}

	if runErr != nil {
		if ctx.Err() != nil {
			return res, &GitError{Code: ErrCodeCancelled, Message: "The operation was cancelled."}
		}

		if runCtx.Err() != nil && ctx.Err() == nil && runCtx.Err() == context.DeadlineExceeded {
			return res, &GitError{Code: ErrCodeUnknown, Message: "Git command timed out."}
		}
	}

	if opts.onProgressLine != nil {
		for _, line := range strings.FieldsFunc(res.stderr, func(r rune) bool { return r == '\n' || r == '\r' }) {
			trimmed := strings.TrimSpace(line)

			if trimmed != "" {
				opts.onProgressLine(trimmed)
			}
		}
	}

	res.exit = 0

	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			res.exit = ee.ExitCode()
		} else {
			res.exit = -1
		}
	}

	if res.exit == 0 || opts.allowFailure {
		return res, nil
	}

	gerr := ClassifyGitError(stderrOrStdout(res.stderr, res.stdout), res.exit)

	if gerr.Code == ErrCodeMergeConflict && len(gerr.Files) == 0 {
		// git prints `CONFLICT … Merge conflict in <path>` on STDOUT while
		// the classified keywords live on stderr — so the file list pulls
		// from both streams (one up on the Rust port, which lost them here).
		gerr.Files = extractConflictFiles(res.stderr + "\n" + res.stdout)

		if len(gerr.Files) == 0 {
			gerr.Files = nil
		}
	}

	return res, gerr
}

func stderrOrStdout(stderr, stdout string) string {
	if strings.TrimSpace(stderr) == "" {
		return stdout
	}

	return stderr
}

// streaming adds `--progress` for remote ops and pipes stderr lines into
// onProgress (mirrors run_git_streaming).
func runStreaming(ctx context.Context, cwd string, opts runOpts) (actionRunResult, *GitError) {
	if len(opts.args) > 0 {
		switch opts.args[0] {
		case "fetch", "push", "pull", "clone":
			if !sliceContains(opts.args, "--progress", "--no-progress") {
				opts.args = append(append([]string{}, opts.args...), "--progress")
			}
		}
	}

	return runActionGit(ctx, cwd, opts)
}

func sliceContains(list []string, needles ...string) bool {
	for _, n := range needles {
		if slices.Contains(list, n) {
			return true
		}
	}

	return false
}

// formatGitCommand renders one argv as a `git …` row for the command log.
func formatGitCommand(argv []string) string {
	var b strings.Builder
	b.WriteString("git")

	for _, a := range argv {
		b.WriteByte(' ')
		needsQuote := strings.ContainsAny(a, " \t\"'")

		if needsQuote {
			b.WriteByte('"')
			b.WriteString(strings.ReplaceAll(a, `"`, `\"`))
			b.WriteByte('"')
		} else {
			b.WriteString(a)
		}
	}

	return b.String()
}

func truncateForLog(text string) (string, bool) {
	if len(text) <= maxCommandLogChars {
		return text, false
	}

	end := maxCommandLogChars

	for end > 0 && (text[end]&(0x80) != 0 && (text[end]&0xC0) != 0xC0) {
		end--
	}

	return text[:end] + "…", true
}

type commandLog struct {
	mu       sync.Mutex
	commands []ExecutedGitCommand
}

// record mirrors record_result: caps at 50 entries, truncates streams.
func (l *commandLog) record(argv []string, res actionRunResult, err *GitError) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.commands) >= maxCommandLogEntries {
		return
	}

	if err == nil {
		stdout, t1 := truncateForLog(res.stdout)
		stderr, t2 := truncateForLog(res.stderr)
		exit := res.exit
		l.commands = append(l.commands, ExecutedGitCommand{
			Argv: argv, Command: formatGitCommand(argv),
			Stdout: stdout, Stderr: stderr,
			ExitCode: &exit, OK: true, Truncated: t1 || t2,
		})

		return
	}

	stderr, truncated := truncateForLog(err.Message)
	l.commands = append(l.commands, ExecutedGitCommand{
		Argv: argv, Command: formatGitCommand(argv),
		Stderr: stderr, OK: false, Truncated: truncated,
	})
}

// envelopeOK builds a success ActionEnvelope.
func envelopeOK(message string) ActionEnvelope {
	return ActionEnvelope{OK: true, Message: message}
}

func envelopeErr(err *GitError) ActionEnvelope {
	conflicts := err.Files

	if len(conflicts) == 0 {
		conflicts = nil
	}

	return ActionEnvelope{OK: false, Message: err.Message, Error: err.Serialize(), Conflicts: conflicts}
}

func metaString(meta map[string]any, key string) (string, bool) {
	if meta == nil {
		return "", false
	}

	if v, ok := meta[key]; ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}

	return "", false
}

func metaBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}

	v, ok := meta[key]

	if !ok {
		return false
	}

	b, ok := v.(bool)

	return ok && b
}

// / rejectOptionLike blocks values that could be parsed as CLI flags or carry
// / embedded newlines (mirror of actions.rs::reject_option_like).
func rejectOptionLike(value, what string) *GitError {
	trimmed := strings.TrimSpace(value)

	if trimmed == "" {
		return unknownErr(what + " is required.")
	}

	if strings.HasPrefix(trimmed, "-") || strings.ContainsAny(trimmed, "\x00\n\r") {
		return unknownErr("Invalid " + what + ".")
	}

	return nil
}

func checkedTarget(target, what string) (string, *GitError) {
	value := strings.TrimSpace(target)

	if value == "" {
		return "", unknownErr(what + " is required.")
	}

	if gerr := rejectOptionLike(value, what); gerr != nil {
		return "", gerr
	}

	return value, nil
}

func checkedMeta(meta map[string]any, key, what string) (string, *GitError) {
	value, _ := metaString(meta, key)
	value = strings.TrimSpace(value)

	if value == "" {
		return "", unknownErr(what + " is required.")
	}

	if gerr := rejectOptionLike(value, what); gerr != nil {
		return "", gerr
	}

	return value, nil
}

// checkedRev validates a rev spec: short, no whitespace/control chars, no
// leading flag. Git remains the final validator.
func checkedRev(value, what string) (string, *GitError) {
	trimmed := strings.TrimSpace(value)

	if gerr := rejectOptionLike(trimmed, what); gerr != nil {
		return "", gerr
	}

	if len(trimmed) > 256 || strings.ContainsFunc(trimmed, func(r rune) bool { return r == ' ' || r == '\t' }) {
		return "", unknownErr("Invalid " + what + ".")
	}

	return trimmed, nil
}

func isLFSMissing(message string) bool {
	m := strings.ToLower(message)

	return strings.Contains(m, "not a git command") ||
		(strings.Contains(m, "lfs") && strings.Contains(m, "not found"))
}

func lfsError(err *GitError) *GitError {
	if isLFSMissing(err.Message) {
		return unknownErr("Git LFS is not installed. Install git-lfs, then retry.")
	}

	return err
}

// remoteTimeoutFor mirrors remote_timeout_for.
func remoteTimeoutFor(kind string) time.Duration {
	switch kind {
	case "fetch", "fetch-all":
		return remoteFetchTimeout

	case "pull", "push", "push-tags", "force-push":
		return remotePushPullTimeout

	default:
		return 0
	}
}

// rebaseEditorBundle pre-builds the todo list + reword messages so a headless
// `git rebase -i` can run without an external editor (port of
// create_rebase_editor_bundle's unix branch).
type rebaseEditorBundle struct {
	dir  string
	env  map[string]string
	done bool
}

func (b *rebaseEditorBundle) cleanup() {
	if !b.done && b.dir != "" {
		_ = os.RemoveAll(b.dir)
		b.done = true
	}
}

func todoLine(action, sha, subject string) string {
	clean := func(s string) string {
		s = strings.ReplaceAll(s, "\r", " ")

		return strings.ReplaceAll(s, "\n", " ")
	}

	return fmt.Sprintf("%s %s %s\n", clean(action), clean(sha), clean(subject))
}

func createRebaseEditorBundle(entries []rebaseTodoEntry) (*rebaseEditorBundle, *GitError) {
	dir, err := os.MkdirTemp("", "gitforge-rebase-")

	if err != nil {
		return nil, unknownErr("could not create rebase bundle dir: " + err.Error())
	}

	b := &rebaseEditorBundle{dir: dir}

	defer func() {
		if b.dir == "" { // failed mid-build: clean partial dir
			b.dir = dir
			b.cleanup()
		}
	}()

	var todo strings.Builder

	for _, e := range entries {
		todo.WriteString(todoLine(e.action, e.sha, e.subject))
	}

	todoPath := filepath.Join(dir, "todo")

	if werr := os.WriteFile(todoPath, []byte(todo.String()), 0o644); werr != nil {
		return nil, unknownErr("could not write rebase todo: " + werr.Error())
	}

	msgDir := filepath.Join(dir, "msgs")

	if merr := os.MkdirAll(msgDir, 0o755); merr != nil {
		return nil, unknownErr("could not create rebase msgs dir: " + merr.Error())
	}

	i := 0

	for _, e := range entries {
		if e.message == "" {
			continue
		}

		text := e.message

		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}

		if werr := os.WriteFile(filepath.Join(msgDir, fmt.Sprintf("msg-%d", i)), []byte(text), 0o644); werr != nil {
			return nil, unknownErr("could not write reword message: " + werr.Error())
		}

		i++
	}

	if werr := os.WriteFile(filepath.Join(dir, "reword-index"), []byte("0"), 0o644); werr != nil {
		return nil, unknownErr("could not write reword index: " + werr.Error())
	}

	seqScript := filepath.Join(dir, "seq-editor.sh")

	if werr := os.WriteFile(seqScript, []byte(fmt.Sprintf("#!/bin/sh\ncp \"%s\" \"$1\"\n", todoPath)), 0o755); werr != nil {
		return nil, unknownErr("could not write seq editor: " + werr.Error())
	}

	msgScript := filepath.Join(dir, "msg-editor.sh")
	msgSrc := fmt.Sprintf("#!/bin/sh\nIDX=$(cat \"%s\")\nIDX=${IDX:-0}\ncp \"%s/msg-$IDX\" \"$1\"\necho $((IDX + 1)) > \"%s\"\n",
		filepath.Join(dir, "reword-index"), msgDir, filepath.Join(dir, "reword-index"))

	if werr := os.WriteFile(msgScript, []byte(msgSrc), 0o755); werr != nil {
		return nil, unknownErr("could not write msg editor: " + werr.Error())
	}

	env := map[string]string{
		"GIT_SEQUENCE_EDITOR": "sh " + seqScript,
		"GIT_EDITOR":          "sh " + msgScript,
	}

	return &rebaseEditorBundle{dir: dir, env: env}, nil
}

type rebaseTodoEntry struct {
	action  string
	sha     string
	subject string
	message string
}

// ExecuteAction mirrors execute_action: checkpoint, route, push-undo,
// progress envelopes. Always returns an envelope (errors inside).
func ExecuteAction(ctx context.Context, repoPath, kind, target string, meta map[string]any) ActionEnvelope {
	operationID := newOperationID()
	runCtx := inFlight.register(ctx, operationID)
	defer inFlight.unregister(operationID)

	emit := func(phase string, percent *int, done bool) {
		hub.Publish("git:progress", mustJSON(OperationProgressWire{
			OperationID: operationID,
			Kind:        kind,
			Phase:       phase,
			Percent:     percent,
			Done:        done,
		}))
	}

	intPtr := func(v int) *int { return &v }

	repo := repoPath
	gitDir := gitDirOrNil(repo)
	branchRes, _ := RunGit(ctx, repo, "branch", "--show-current")
	detached := strings.TrimSpace(branchRes.Stdout) == ""
	repoState := detectRepoState(repo, gitDir, detached)

	squashMerge := kind == "merge" && metaBool(meta, "squash")
	captureKind := journalCaptureHead

	if !squashMerge {
		captureKind = journalCaptureFor(kind)
	}

	log := &commandLog{}
	message := ""
	var routeErr *GitError

	if captureKind != journalCaptureNone {
		// Checkpoint failures don't block the action (mirror: `if let Ok`).
		if cp, gerr := journalFor(repo).Capture(ctx, repo, captureKind); gerr == nil {
			switch kind {
			case "reset-soft":
				cp.ResetMode = "soft"

			case "reset-mixed":
				cp.ResetMode = "mixed"

			case "reset-hard":
				cp.ResetMode = "hard"
			}

			defer func() {
				if routeErr == nil && isJournalableAction(kind) {
					journalFor(repo).PushUndo(cp, journalLabel(kind, target, metaNameFor(meta)))
				}
			}()
		}
	}

	emit("Starting…", intPtr(0), false)
	progressCb := func(line string) { emit(line, nil, false) }
	message, routeErr = routeAction(actionContext{
		ctx:        runCtx,
		repo:       repo,
		onProgress: progressCb,
		repoState:  repoState,
		journal:    journalFor(repo),
		log:        log,
	}, kind, target, meta)
	emit("Done", intPtr(100), true)

	env := ActionEnvelope{OK: routeErr == nil, Message: message}

	if routeErr != nil {
		env.Message = routeErr.Message
		env.Error = routeErr.Serialize()
		env.Conflicts = routeErr.Files

		if len(env.Conflicts) == 0 {
			env.Conflicts = nil
		}
	}

	env.Commands = log.commands

	if len(env.Commands) == 0 {
		env.Commands = nil
	}

	return env
}

func metaNameFor(meta map[string]any) string {
	name, _ := metaString(meta, "name")

	return name
}

func newOperationID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])

	return fmt.Sprintf("%x-%x-%x", b[0:4], b[4:8], b[8:16])
}

// actionContext carries the routing dependencies (the Rust code passed many
// &-params; Go gets a single struct).
type actionContext struct {
	ctx        context.Context
	repo       string
	onProgress func(line string)
	repoState  RepoState
	journal    *Journal
	log        *commandLog
}

// run executes one argv and records it into the command log.
func (c *actionContext) run(args []string) (actionRunResult, *GitError) {
	res, gerr := runActionGit(c.ctx, c.repo, runOpts{args: args})
	c.log.record(args, res, gerr)

	return res, gerr
}

func (c *actionContext) runAllow(args []string) (actionRunResult, *GitError) {
	res, gerr := runActionGit(c.ctx, c.repo, runOpts{args: args, allowFailure: true})
	c.log.record(args, res, gerr)

	return res, gerr
}

// runStream mirrors run_stream: remote op + streaming progress.
func (c *actionContext) runStream(args []string, op string) (actionRunResult, *GitError) {
	res, gerr := runStreaming(c.ctx, c.repo, runOpts{
		args:           args,
		timeout:        remoteTimeoutFor(op),
		onProgressLine: c.onProgress,
	})
	c.log.record(args, res, gerr)

	return res, gerr
}

func (c *actionContext) noTarget() *GitError {
	return unknownErr("No target specified.")
}

// currentBranch resolves HEAD's short branch name. Detached HEAD surfaces
// as "HEAD" — an explicit error rather than a silent mis-push.
func (c *actionContext) currentBranch() (string, *GitError) {
	res, gerr := c.run([]string{"rev-parse", "--abbrev-ref", "HEAD"})

	if gerr != nil {
		return "", gerr
	}

	branch := strings.TrimSpace(res.stdout)

	if branch == "" || branch == "HEAD" {
		return "", unknownErr("Not on a branch — check out a branch before publishing it.")
	}

	return branch, nil
}

// routeAction mirrors route_action: the kind → git argv dispatch table.
func routeAction(c actionContext, kind, target string, meta map[string]any) (string, *GitError) {
	// Semantic validation (argv arrays prevent shell injection, but a value
	// like `-h` would still parse as a git flag).
	if target != "" {
		refKinds := map[string]bool{
			"checkout": true, "merge": true, "rebase": true, "cherry-pick": true,
			"reset-soft": true, "reset-mixed": true, "reset-hard": true,
			"revert": true, "create-branch": true, "rename-branch": true,
			"delete-branch": true, "force-delete-branch": true, "set-upstream": true,
			"delete-tag": true, "create-tag": true, "bisect-good": true, "bisect-bad": true,
		}

		if refKinds[kind] {
			if gerr := rejectOptionLike(strings.TrimSpace(target), "target"); gerr != nil {
				return "", gerr
			}
		}

		if kind == "stage-patch" || kind == "discard-patch" {
			patch, _ := metaString(meta, "patch")

			if !strings.HasPrefix(patch, "diff --git") || strings.Contains(patch, "\x00") {
				return "", unknownErr("Invalid patch.")
			}
		}
	}

	switch kind {
	// remote
	case "fetch":
		if _, gerr := c.runStream([]string{"fetch", "--all"}, "fetch"); gerr != nil {
			return "", gerr
		}

		return "Fetched from all remotes", nil

	case "fetch-all":
		if _, gerr := c.runStream([]string{"fetch", "--all", "--prune"}, "fetch-all"); gerr != nil {
			return "", gerr
		}

		return "Fetched and pruned remotes", nil

	case "pull":
		if _, gerr := c.runStream([]string{"pull", "--ff-only"}, "pull"); gerr != nil {
			return "", gerr
		}

		return "Pulled latest changes", nil

	case "push":
		if _, gerr := c.runStream([]string{"push"}, "push"); gerr != nil {
			return "", gerr
		}

		return "Pushed to remote", nil

	case "force-push":
		if _, gerr := c.runStream([]string{"push", "--force-with-lease"}, "force-push"); gerr != nil {
			return "", gerr
		}

		return "Force-pushed to remote", nil

	case "push-upstream":
		branch, gerr := c.currentBranch()

		if gerr != nil {
			return "", gerr
		}

		remote, _ := metaString(meta, "remote")
		remote = strings.TrimSpace(remote)

		if remote == "" {
			remote = "origin"
		}

		// Optional publish-as rename: push the local branch under a
		// different name on the remote via refspec `<branch>:<remoteBranch>`.
		remoteBranch, _ := metaString(meta, "remoteBranch")
		remoteBranch = strings.TrimSpace(remoteBranch)

		if remoteBranch == "" {
			remoteBranch = branch
		}

		if gerr := rejectOptionLike(remote, "remote"); gerr != nil {
			return "", gerr
		}

		if gerr := rejectOptionLike(branch, "branch"); gerr != nil {
			return "", gerr
		}

		if gerr := rejectOptionLike(remoteBranch, "remote branch"); gerr != nil {
			return "", gerr
		}

		refspec := branch

		if remoteBranch != branch {
			refspec = branch + ":" + remoteBranch
		}

		if _, gerr := c.runStream([]string{"push", "--set-upstream", remote, refspec}, "push"); gerr != nil {
			return "", gerr
		}

		return "Created " + remote + "/" + remoteBranch + " and pushed", nil

	case "push-tags":
		if _, gerr := c.runStream([]string{"push", "--tags"}, "push-tags"); gerr != nil {
			return "", gerr
		}

		return "Pushed tags", nil

	case "prune":
		remote, _ := metaString(meta, "remote")
		remote = strings.TrimSpace(remote)

		if remote == "" {
			remote = "origin"
		}

		if gerr := rejectOptionLike(remote, "remote"); gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"remote", "prune", remote}); gerr != nil {
			return "", gerr
		}

		return "Pruned stale remote branches (" + remote + ")", nil

	case "sync":
		if _, gerr := c.runStream([]string{"fetch", "--all"}, "fetch"); gerr != nil {
			return "", gerr
		}

		if _, gerr := c.runStream([]string{"pull", "--ff-only"}, "pull"); gerr != nil {
			return "", gerr
		}

		return "Synced with remote", nil

	// stash
	case "stash":
		msg, _ := metaString(meta, "message")

		if msg == "" {
			msg = "WIP"
		}

		args := []string{"stash", "push", "-m", msg}

		if metaBool(meta, "includeUntracked") {
			args = append(args, "-u")
		}

		if metaBool(meta, "keepIndex") {
			args = append(args, "-k")
		}

		if _, gerr := c.run(args); gerr != nil {
			return "", gerr
		}

		return "Saved working tree to stash", nil

	case "pop-stash":
		stashRef, gerr := checkedRev(derefEmpty(target, "stash@{0}"), "stash")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"stash", "pop", stashRef}); gerr != nil {
			return "", gerr
		}

		return "Popped stash", nil

	case "apply-stash":
		stashRef, gerr := checkedRev(derefEmpty(target, "stash@{0}"), "stash")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"stash", "apply", stashRef}); gerr != nil {
			return "", gerr
		}

		return "Applied stash", nil

	case "drop-stash":
		stashRef, gerr := checkedRev(derefEmpty(target, "stash@{0}"), "stash")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"stash", "drop", stashRef}); gerr != nil {
			return "", gerr
		}

		return "Dropped stash", nil

	case "rename-stash":
		stashRef, gerr := checkedRev(derefEmpty(target, "stash@{0}"), "stash")

		if gerr != nil {
			return "", gerr
		}

		message := strings.TrimSpace(metaOr(meta, "message", ""))

		if message == "" {
			return "", unknownErr("Stash message is required.")
		}

		rev, gerr := c.run([]string{"rev-parse", stashRef})

		if gerr != nil {
			return "", gerr
		}

		sha := strings.TrimSpace(rev.stdout)

		if sha == "" {
			return "", unknownErr("Could not resolve stash ref.")
		}

		// `stash store` with the same sha while the ref exists is a no-op,
		// so drop first (sha stays in the object db) then re-store.
		if _, gerr := c.run([]string{"stash", "drop", stashRef}); gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"stash", "store", "-m", message, sha}); gerr != nil {
			return "", unknownErr(
				"Renamed stash was dropped but re-storing failed (recover with " + sha + ").")
		}

		return "Renamed stash", nil

	// working tree
	case "stage":
		t, gerr := requireTarget(target, "No file specified.")

		if gerr != nil {
			return "", gerr
		}

		safe, gerr := assertPathInsideRepo(c.repo, t)

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"add", "--", safe}); gerr != nil {
			return "", gerr
		}

		return "Staged " + t, nil

	case "unstage":
		t, gerr := requireTarget(target, "No file specified.")

		if gerr != nil {
			return "", gerr
		}

		safe, gerr := assertPathInsideRepo(c.repo, t)

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"restore", "--staged", "--", safe}); gerr != nil {
			return "", gerr
		}

		return "Unstaged " + t, nil

	case "stage-all":
		if _, gerr := c.run([]string{"add", "-A"}); gerr != nil {
			return "", gerr
		}

		return "Staged all changes", nil

	case "unstage-all":
		if _, gerr := c.run([]string{"restore", "--staged", "--", "."}); gerr != nil {
			return "", gerr
		}

		return "Unstaged all changes", nil

	case "discard-file":
		t, gerr := requireTarget(target, "No file specified.")

		if gerr != nil {
			return "", gerr
		}

		safe, gerr := assertPathInsideRepo(c.repo, t)

		if gerr != nil {
			return "", gerr
		}

		_, attempt := c.run([]string{"restore", "--source=HEAD", "--staged", "--worktree", "--", safe})

		if attempt != nil {
			// Untracked files are unknown to git — restore fails with a
			// pathspec error, so delete them from disk instead.
			msg := strings.ToLower(attempt.Message)

			if strings.Contains(msg, "did not match any file") && strings.Contains(msg, "known to git") {
				full := filepath.Join(c.repo, safe)

				if fi, serr := os.Stat(full); serr == nil {
					if fi.IsDir() {
						_ = os.RemoveAll(full)
					} else {
						_ = os.Remove(full)
					}
				}

				return "Discarded changes in " + t, nil
			}

			return "", attempt
		}

		return "Discarded changes in " + t, nil

	case "discard-all":
		if _, gerr := c.run([]string{"restore", "--source=HEAD", "--staged", "--worktree", "--", "."}); gerr != nil {
			return "", gerr
		}

		return "Discarded all changes", nil

	case "restore-file":
		t, gerr := requireTarget(target, "No file specified.")

		if gerr != nil {
			return "", gerr
		}

		sha, gerr := checkedRev(metaOr(meta, "sha", "HEAD"), "revision")

		if gerr != nil {
			return "", gerr
		}

		safe, gerr := assertPathInsideRepo(c.repo, t)

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"restore", "--source", sha, "--", safe}); gerr != nil {
			return "", gerr
		}

		return "Restored " + t + " from " + truncateSha(sha, 7), nil

	case "stage-patch", "unstage-patch":
		t, gerr := requireTarget(target, "No file specified.")

		if gerr != nil {
			return "", gerr
		}

		patch, _ := metaString(meta, "patch")

		if strings.TrimSpace(patch) == "" {
			return "", unknownErr("No patch specified.")
		}

		safe, gerr := assertPathInsideRepo(c.repo, t)

		if gerr != nil {
			return "", gerr
		}

		if metaBool(meta, "intentToAdd") {
			if _, gerr := c.runAllow([]string{"add", "-N", "--", safe}); gerr != nil {
				return "", gerr
			}
		}

		if !strings.HasPrefix(patch, "diff --git") {
			return "", unknownErr("Invalid patch.")
		}

		applyArgs := []string{"apply", "--cached", "--include=" + safe}

		if kind == "unstage-patch" {
			applyArgs = append(applyArgs, "-R")
		}

		res, gerr := runActionGit(c.ctx, c.repo, runOpts{args: applyArgs, input: patch})
		c.log.record(applyArgs, res, gerr)

		if gerr != nil {
			return "", gerr
		}

		if kind == "unstage-patch" {
			return "Unstaged hunk in " + t, nil
		}

		return "Staged hunk in " + t, nil

	case "discard-patch":
		t, gerr := requireTarget(target, "No file specified.")

		if gerr != nil {
			return "", gerr
		}

		patch, _ := metaString(meta, "patch")

		if strings.TrimSpace(patch) == "" {
			return "", unknownErr("No patch specified.")
		}

		safe, gerr := assertPathInsideRepo(c.repo, t)

		if gerr != nil {
			return "", gerr
		}

		if metaBool(meta, "intentToAdd") {
			if _, gerr := c.runAllow([]string{"add", "-N", "--", safe}); gerr != nil {
				return "", gerr
			}
		}

		if !strings.HasPrefix(patch, "diff --git") {
			return "", unknownErr("Invalid patch.")
		}

		applyArgs := []string{"apply", "-R", "--include=" + safe}
		res, gerr := runActionGit(c.ctx, c.repo, runOpts{args: applyArgs, input: patch})
		c.log.record(applyArgs, res, gerr)

		if gerr != nil {
			return "", gerr
		}

		return "Discarded hunk in " + t, nil

	// merge conflicts
	case "resolve-conflict-ours", "resolve-conflict-theirs":
		t, gerr := requireTarget(target, "No file specified.")

		if gerr != nil {
			return "", gerr
		}

		safe, gerr := assertPathInsideRepo(c.repo, t)

		if gerr != nil {
			return "", gerr
		}

		side := "--ours"

		if kind == "resolve-conflict-theirs" {
			side = "--theirs"
		}

		if _, gerr := c.run([]string{"checkout", side, "--", safe}); gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"add", "--", safe}); gerr != nil {
			return "", gerr
		}

		label := "ours"

		if kind == "resolve-conflict-theirs" {
			label = "theirs"
		}

		return "Resolved " + t + " using " + label, nil

	case "resolve-conflict-block":
		t, gerr := requireTarget(target, "No file specified.")

		if gerr != nil {
			return "", gerr
		}

		safe, gerr := assertPathInsideRepo(c.repo, t)

		if gerr != nil {
			return "", gerr
		}

		blockIndex := -1

		if v, ok := meta["blockIndex"].(float64); ok {
			blockIndex = int(v)
		}

		side := "ours"

		if s, _ := metaString(meta, "side"); s == "theirs" {
			side = "theirs"
		}

		if blockIndex < 0 {
			return "", unknownErr("Invalid conflict block index.")
		}

		full := filepath.Join(c.repo, safe)
		content, rerr := os.ReadFile(full)

		if rerr != nil {
			return "", unknownErr("could not read conflicted file: " + rerr.Error())
		}

		updated, cerr := applyConflictResolution(string(content), blockIndex, side)

		if cerr != nil {
			return "", unknownErr(cerr.Error())
		}

		if werr := os.WriteFile(full, []byte(updated), 0o644); werr != nil {
			return "", unknownErr("could not write conflicted file: " + werr.Error())
		}

		if !parseConflictMarkers(updated).hasMarkers {
			if _, gerr := c.run([]string{"add", "--", safe}); gerr != nil {
				return "", gerr
			}
		}

		return "Resolved conflict block in " + t, nil

	case "mark-conflict-resolved":
		t, gerr := requireTarget(target, "No file specified.")

		if gerr != nil {
			return "", gerr
		}

		safe, gerr := assertPathInsideRepo(c.repo, t)

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"add", "--", safe}); gerr != nil {
			return "", gerr
		}

		return "Marked " + t + " as resolved", nil

	// commit
	case "commit":
		title := strings.TrimSpace(metaOr(meta, "message", ""))

		if title == "" {
			return "", &GitError{Code: ErrCodeNothingToCommit, Message: "Commit message is required."}
		}

		args := []string{"commit", "-m", title}

		if body := strings.TrimSpace(metaOr(meta, "body", "")); body != "" {
			args = append(args, "-m", body)
		}

		if metaBool(meta, "signOff") {
			args = append(args, "--signoff")
		}

		if metaBool(meta, "sign") {
			args = append(args, "-S")
		}

		if _, gerr := c.run(args); gerr != nil {
			return "", gerr
		}

		return "Created commit", nil

	case "amend":
		title := strings.TrimSpace(metaOr(meta, "message", ""))
		args := []string{"commit", "--amend", "--no-edit"}

		if title != "" {
			args = []string{"commit", "--amend", "-m", title}

			if body := strings.TrimSpace(metaOr(meta, "body", "")); body != "" {
				args = append(args, "-m", body)
			}
		}

		if metaBool(meta, "signOff") {
			args = append(args, "--signoff")
		}

		if metaBool(meta, "sign") {
			args = append(args, "-S")
		}

		if _, gerr := c.run(args); gerr != nil {
			return "", gerr
		}

		return "Amended commit", nil

	case "commit-and-push", "commit-and-force-push":
		commitKind := "commit"

		if metaBool(meta, "amend") {
			commitKind = "amend"
		}

		if _, gerr := routeAction(c, commitKind, target, meta); gerr != nil {
			return "", gerr
		}

		pushKind := "push"

		if kind == "commit-and-force-push" {
			pushKind = "force-push"
		}

		if _, gerr := routeAction(c, pushKind, "", meta); gerr != nil {
			return "", gerr
		}

		if metaBool(meta, "amend") {
			if kind == "commit-and-force-push" {
				return "Amended and force-pushed", nil
			}

			return "Amended and pushed", nil
		}

		if kind == "commit-and-force-push" {
			return "Committed and force-pushed", nil
		}

		return "Committed and pushed", nil

	// branches
	case "checkout":
		t, gerr := checkedTarget(target, "branch")

		if gerr != nil {
			return "", gerr
		}

		if remote, _ := metaString(meta, "remote"); strings.TrimSpace(remote) != "" {
			remote = strings.TrimSpace(remote)

			if gerr := rejectOptionLike(remote, "remote"); gerr != nil {
				return "", gerr
			}

			remoteRef := remote + "/" + t
			created, gerr := c.runAllow([]string{"checkout", "-b", t, remoteRef})

			if gerr != nil {
				return "", gerr
			}

			if created.exit != 0 {
				if _, gerr := c.run([]string{"checkout", t}); gerr != nil {
					return "", gerr
				}
			}

			return "Checked out " + t, nil
		}

		if _, gerr := c.run([]string{"checkout", t}); gerr != nil {
			return "", gerr
		}

		return "Checked out " + t, nil

	case "checkout-commit":
		if target == "" {
			return "", c.noTarget()
		}

		rev, gerr := checkedRev(target, "commit")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"checkout", "--detach", rev}); gerr != nil {
			return "", gerr
		}

		return "Checked out " + truncateSha(rev, 7) + " (detached HEAD)", nil

	case "create-branch":
		name := strings.TrimSpace(metaOr(meta, "name", ""))

		if name != "" {
			if gerr := rejectOptionLike(name, "branch name"); gerr != nil {
				return "", gerr
			}
		} else {
			var kgerr *GitError
			name, kgerr = checkedTarget(target, "branch name")

			if kgerr != nil {
				return "", kgerr
			}
		}

		if start, _ := metaString(meta, "startPoint"); strings.TrimSpace(start) != "" {
			rev, sgerr := checkedRev(start, "start point")

			if sgerr != nil {
				return "", sgerr
			}

			if _, gerr := c.run([]string{"checkout", "-b", name, rev}); gerr != nil {
				return "", gerr
			}
		} else {
			if _, gerr := c.run([]string{"checkout", "-b", name}); gerr != nil {
				return "", gerr
			}
		}

		return "Created and checked out " + name, nil

	case "rename-branch":
		newName := strings.TrimSpace(metaOr(meta, "newName", metaOr(meta, "name", "")))

		if newName == "" {
			return "", unknownErr("Branch names required.")
		}

		if gerr := rejectOptionLike(newName, "branch name"); gerr != nil {
			return "", gerr
		}

		t, gerr := checkedTarget(target, "branch")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"branch", "-m", t, newName}); gerr != nil {
			return "", gerr
		}

		return "Renamed branch to " + newName, nil

	case "delete-branch":
		t, gerr := checkedTarget(target, "branch")

		if gerr != nil {
			return "", gerr
		}

		flag := "-d"

		if metaBool(meta, "force") {
			flag = "-D"
		}

		if _, gerr := c.run([]string{"branch", flag, t}); gerr != nil {
			return "", gerr
		}

		return "Deleted branch " + t, nil

	case "delete-remote-branch":
		t, gerr := checkedTarget(target, "remote branch")

		if gerr != nil {
			return "", gerr
		}

		explicit := strings.TrimSpace(metaOr(meta, "remote", ""))

		if explicit != "" {
			if gerr := rejectOptionLike(explicit, "remote"); gerr != nil {
				return "", gerr
			}
		}

		remote, branch := "origin", t

		if explicit != "" {
			remote = explicit
		} else if slash := strings.Index(t, "/"); slash > 0 {
			remote, branch = t[:slash], t[slash+1:]
		}

		if branch == "" {
			return "", unknownErr("No remote branch specified.")
		}

		if gerr := rejectOptionLike(remote, "remote"); gerr != nil {
			return "", gerr
		}

		if gerr := rejectOptionLike(branch, "branch"); gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"push", remote, "--delete", branch}); gerr != nil {
			return "", gerr
		}

		return "Deleted remote branch " + branch, nil

	case "set-upstream":
		t, gerr := checkedTarget(target, "upstream")

		if gerr != nil {
			return "", gerr
		}

		// Upstream must be `<remote>/<branch>`; validate both halves.
		remote, branch, ok := strings.Cut(t, "/")

		if !ok || remote == "" || branch == "" {
			return "", unknownErr("Upstream must be <remote>/<branch>.")
		}

		if gerr := rejectOptionLike(remote, "remote"); gerr != nil {
			return "", gerr
		}

		if gerr := rejectOptionLike(branch, "branch"); gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"branch", "-u", t}); gerr != nil {
			return "", gerr
		}

		return "Set upstream to " + t, nil

	case "compare-branches":
		t, gerr := checkedTarget(target, "branch")

		if gerr != nil {
			return "", gerr
		}

		other, gerr := checkedMeta(meta, "other", "branch")

		if gerr != nil {
			return "", gerr
		}

		out, gerr := c.run([]string{"log", "--oneline", t + ".." + other})

		if gerr != nil {
			return "", gerr
		}

		count := 0

		for _, line := range strings.Split(out.stdout, "\n") {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}

		return fmt.Sprintf("%d commits in %s not in %s", count, other, t), nil

	// worktrees
	case "worktree-add":
		wtPath, _ := metaString(meta, "path")

		if strings.TrimSpace(wtPath) == "" {
			return "", unknownErr("Worktree path required.")
		}

		if gerr := rejectOptionLike(strings.TrimSpace(wtPath), "worktree path"); gerr != nil {
			return "", gerr
		}

		args := []string{"worktree", "add", wtPath}

		if branch, _ := metaString(meta, "branch"); strings.TrimSpace(branch) != "" {
			branch = strings.TrimSpace(branch)

			if gerr := rejectOptionLike(branch, "branch"); gerr != nil {
				return "", gerr
			}

			args = append(args, branch)
		}

		if _, gerr := c.run(args); gerr != nil {
			return "", gerr
		}

		return "Added worktree at " + wtPath, nil

	case "worktree-remove":
		t, gerr := checkedTarget(target, "worktree path")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"worktree", "remove", t}); gerr != nil {
			return "", gerr
		}

		return "Removed worktree at " + t, nil

	// LFS
	case "lfs-track":
		t, gerr := checkedTarget(target, "tracking pattern")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"lfs", "track", t}); gerr != nil {
			return "", lfsError(gerr)
		}

		return "Tracking " + t + " with Git LFS", nil

	case "lfs-untrack":
		t, gerr := checkedTarget(target, "tracking pattern")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"lfs", "untrack", t}); gerr != nil {
			return "", lfsError(gerr)
		}

		return "Stopped tracking " + t + " with Git LFS", nil

	case "lfs-prune":
		if _, gerr := c.run([]string{"lfs", "prune"}); gerr != nil {
			return "", lfsError(gerr)
		}

		return "Pruned local Git LFS objects", nil

	// merge
	case "merge":
		t, gerr := checkedTarget(target, "branch")

		if gerr != nil {
			return "", gerr
		}

		args := []string{}

		switch {
		case metaBool(meta, "ffOnly"):
			args = append(args, "--ff-only")

		case metaBool(meta, "noFf"):
			args = append(args, "--no-ff")

		case metaBool(meta, "squash"):
			args = append(args, "--squash")
		}

		args = append(args, t)

		if _, gerr := c.run(append([]string{"merge"}, args...)); gerr != nil {
			return "", gerr
		}

		if metaBool(meta, "squash") {
			return "Squashed " + t + " — review staged changes and commit", nil
		}

		return "Merged " + t, nil

	case "merge-continue":
		if _, gerr := c.run([]string{"merge", "--continue"}); gerr != nil {
			return "", gerr
		}

		return "Merge completed", nil

	case "merge-abort":
		if _, gerr := c.run([]string{"merge", "--abort"}); gerr != nil {
			return "", gerr
		}

		return "Merge aborted", nil

	// rebase
	case "rebase":
		t, gerr := checkedTarget(target, "upstream")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"rebase", t}); gerr != nil {
			return "", gerr
		}

		return "Rebased onto " + t, nil

	case "rebase-from-here":
		if target == "" {
			return "", unknownErr("No commit specified.")
		}

		rev, gerr := checkedRev(target, "commit")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"rebase", "--onto", rev, "HEAD~1"}); gerr != nil {
			return "", gerr
		}

		return "Rebased from commit", nil

	case "interactive-rebase":
		if target == "" {
			return "", unknownErr("No base specified.")
		}

		base, gerr := checkedRev(target, "base")

		if gerr != nil {
			return "", gerr
		}

		entries, cerr := parseRebaseEntries(meta)

		if cerr != nil {
			return "", cerr
		}

		bundle, gerr := createRebaseEditorBundle(entries)

		if gerr != nil {
			return "", gerr
		}

		defer bundle.cleanup()
		argv := []string{"rebase", "-i", base}
		res, gerr := runActionGit(c.ctx, c.repo, runOpts{
			args:      argv,
			noTimeout: true, // rebase may run long (mirror: None)
			extraEnv:  bundle.env,
		})

		c.log.record(argv, res, gerr)

		if gerr != nil {
			return "", gerr
		}

		return "Interactive rebase started", nil

	case "rebase-continue":
		if _, gerr := c.run([]string{"rebase", "--continue"}); gerr != nil {
			return "", gerr
		}

		return "Rebase continued", nil

	case "rebase-abort":
		if _, gerr := c.run([]string{"rebase", "--abort"}); gerr != nil {
			return "", gerr
		}

		return "Rebase aborted", nil

	case "rebase-skip":
		if _, gerr := c.run([]string{"rebase", "--skip"}); gerr != nil {
			return "", gerr
		}

		return "Rebase skipped commit", nil

	// cherry-pick
	case "cherry-pick":
		if target == "" {
			return "", unknownErr("No commit specified.")
		}

		rev, gerr := checkedRev(target, "commit")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"cherry-pick", rev}); gerr != nil {
			return "", gerr
		}

		return "Cherry-picked " + truncateSha(rev, 7), nil

	case "cherry-pick-continue":
		if _, gerr := c.run([]string{"cherry-pick", "--continue"}); gerr != nil {
			return "", gerr
		}

		return "Cherry-pick continued", nil

	case "cherry-pick-abort":
		if _, gerr := c.run([]string{"cherry-pick", "--abort"}); gerr != nil {
			return "", gerr
		}

		return "Cherry-pick aborted", nil

	// bisect
	case "bisect-start":
		bad, _ := metaString(meta, "bad")

		if bad == "" {
			bad = target
		}

		if strings.TrimSpace(bad) != "" {
			rev, gerr := checkedRev(bad, "revision")

			if gerr != nil {
				return "", gerr
			}

			bad = rev
		} else {
			bad = "HEAD"
		}

		good, _ := metaString(meta, "good")
		var goodRev string

		if strings.TrimSpace(good) != "" {
			goodRev2, gerr := checkedRev(good, "revision")

			if gerr != nil {
				return "", gerr
			}

			goodRev = goodRev2
		}

		if _, gerr := c.run([]string{"bisect", "start"}); gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"bisect", "bad", bad}); gerr != nil {
			_, _ = c.run([]string{"bisect", "reset"})

			return "", gerr
		}

		if goodRev != "" {
			if _, gerr := c.run([]string{"bisect", "good", goodRev}); gerr != nil {
				_, _ = c.run([]string{"bisect", "reset"})

				return "", gerr
			}
		}

		if goodRev != "" {
			return "Bisecting " + bad + " against " + goodRev, nil
		}

		return "Bisect started at " + bad, nil

	case "bisect-good":
		if target != "" {
			rev, gerr := checkedRev(target, "revision")

			if gerr != nil {
				return "", gerr
			}

			if _, gerr := c.run([]string{"bisect", "good", rev}); gerr != nil {
				return "", gerr
			}
		} else if _, gerr := c.run([]string{"bisect", "good"}); gerr != nil {
			return "", gerr
		}

		return "Marked commit as good", nil

	case "bisect-bad":
		if target != "" {
			rev, gerr := checkedRev(target, "revision")

			if gerr != nil {
				return "", gerr
			}

			if _, gerr := c.run([]string{"bisect", "bad", rev}); gerr != nil {
				return "", gerr
			}
		} else if _, gerr := c.run([]string{"bisect", "bad"}); gerr != nil {
			return "", gerr
		}

		return "Marked commit as bad", nil

	case "bisect-skip":
		if _, gerr := c.run([]string{"bisect", "skip"}); gerr != nil {
			return "", gerr
		}

		return "Skipped commit during bisect", nil

	case "bisect-reset":
		if _, gerr := c.run([]string{"bisect", "reset"}); gerr != nil {
			return "", gerr
		}

		return "Bisect ended", nil

	// resets
	case "reset-soft", "reset-mixed", "reset-hard":
		rev, gerr := checkedRev(derefEmpty(target, "HEAD"), "revision")

		if gerr != nil {
			return "", gerr
		}

		var flag string

		switch kind {
		case "reset-soft":
			flag = "--soft"

		case "reset-mixed":
			flag = "--mixed"

		default:
			flag = "--hard"
		}

		if _, gerr := c.run([]string{"reset", flag, rev}); gerr != nil {
			return "", gerr
		}

		switch kind {
		case "reset-soft":
			return "Soft reset complete", nil

		case "reset-mixed":
			return "Mixed reset complete", nil

		default:
			return "Hard reset complete", nil
		}

	// revert
	case "revert":
		if target == "" {
			return "", unknownErr("No commit specified.")
		}

		rev, gerr := checkedRev(target, "commit")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"revert", "--no-edit", rev}); gerr != nil {
			return "", gerr
		}

		return "Reverted " + truncateSha(rev, 7), nil

	case "revert-continue":
		if _, gerr := c.run([]string{"revert", "--continue"}); gerr != nil {
			return "", gerr
		}

		return "Revert continued", nil

	case "revert-abort":
		if _, gerr := c.run([]string{"revert", "--abort"}); gerr != nil {
			return "", gerr
		}

		return "Revert aborted", nil

	// tags
	case "create-tag", "tag":
		name := strings.TrimSpace(metaOr(meta, "name", ""))

		if name != "" {
			if gerr := rejectOptionLike(name, "tag name"); gerr != nil {
				return "", gerr
			}
		} else {
			var kgerr *GitError
			name, kgerr = checkedTarget(target, "tag name")

			if kgerr != nil {
				return "", kgerr
			}
		}

		var args []string

		if metaBool(meta, "annotated") {
			msg := metaOr(meta, "message", name)
			args = []string{"tag", "-a", name, "-m", msg}
		} else {
			args = []string{"tag", name}
		}

		if sha, _ := metaString(meta, "sha"); strings.TrimSpace(sha) != "" {
			sha, gerr := checkedRev(strings.TrimSpace(sha), "revision")

			if gerr != nil {
				return "", gerr
			}

			args = append(args, sha)
		}

		if _, gerr := c.run(args); gerr != nil {
			return "", gerr
		}

		return "Created tag " + name, nil

	case "delete-tag":
		t, gerr := checkedTarget(target, "tag")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"tag", "-d", t}); gerr != nil {
			return "", gerr
		}

		return "Deleted tag " + t, nil

	case "checkout-tag":
		t, gerr := checkedTarget(target, "tag")

		if gerr != nil {
			return "", gerr
		}

		if _, gerr := c.run([]string{"checkout", "--detach", t}); gerr != nil {
			return "", gerr
		}

		return "Checked out tag " + t, nil

	// undo/redo + misc
	case "refresh":
		return "Repository refreshed", nil

	case "undo":
		if !isUndoRedoSafe(c.repoState) {
			return "", unknownErr("Cannot undo while an operation is in progress.")
		}

		return c.journal.Undo(c.ctx, c.repo)

	case "redo":
		if !isUndoRedoSafe(c.repoState) {
			return "", unknownErr("Cannot redo while an operation is in progress.")
		}

		return c.journal.Redo(c.ctx, c.repo)

	default:
		return "", unknownErr("Unsupported action: " + kind)
	}
}

// "open-terminal" | "reveal-in-finder" | "open-remote" | "copy-sha" |
// "open-on-github" are handled in the renderer (mirror);
// `requireTarget` wraps the "No file/branch specified" pattern.
func requireTarget(target, message string) (string, *GitError) {
	if strings.TrimSpace(target) == "" {
		return "", unknownErr(message)
	}

	return target, nil
}

func derefEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}

	return v
}

func metaOr(meta map[string]any, key, fallback string) string {
	if v, ok := metaString(meta, key); ok && v != "" {
		return v
	}

	return fallback
}

// parseRebaseEntries validates the interactive-rebase plan (mirror of the
// entries loop in route_action).
func parseRebaseEntries(meta map[string]any) ([]rebaseTodoEntry, *GitError) {
	raw, _ := meta["entries"].([]any)

	if len(raw) == 0 {
		return nil, unknownErr("No rebase plan specified.")
	}

	if len(raw) > 500 {
		return nil, unknownErr("Rebase plan is too large.")
	}

	allowed := map[string]bool{"pick": true, "reword": true, "edit": true,
		"squash": true, "fixup": true, "drop": true}

	entries := make([]rebaseTodoEntry, 0, len(raw))

	for _, item := range raw {
		e, ok := item.(map[string]any)

		if !ok {
			return nil, unknownErr("Invalid rebase action.")
		}

		action, _ := e["action"].(string)

		if action == "" {
			action = "pick"
		}

		if !allowed[action] {
			return nil, unknownErr("Invalid rebase action.")
		}

		sha, _ := e["sha"].(string)
		sha, gerr := checkedRev(sha, "commit")

		if gerr != nil {
			return nil, gerr
		}

		subject, _ := e["subject"].(string)

		if len(subject) > 4096 {
			return nil, unknownErr("Rebase subject is too long.")
		}

		message, _ := e["message"].(string)
		entries = append(entries, rebaseTodoEntry{action: action, sha: sha, subject: subject, message: message})
	}

	return entries, nil
}

// mustJSON serializes without error path (wire payloads are plain structs).
func mustJSON(v any) string {
	b, err := json.Marshal(v)

	if err != nil {
		return "{}"
	}

	return string(b)
}
