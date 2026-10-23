// Package backend is the gitforge Go git layer: process model, parsers and
// services mirroring the original Tauri/Rust backend (`src-tauri`). Wire
// shapes exactly match `shared/git/models.ts` so the Vue renderer needs no
// rewriting — only `src/lib/wails/wailsGitService.ts` adapts the transport.
package backend

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// GitResult captures one git CLI invocation. Unlike exec.Command, nonzero
// exits are data (mirrors the Rust git_text contract), so callers decide what
// failure means.
type GitResult struct {
	Stdout  string
	Stderr  string
	Exit    int  // command exit code; 0 means success
	Spawned bool // false when the git binary could not be started at all
}

func (r GitResult) Ok() bool { return r.Spawned && r.Exit == 0 }

// RunGit runs `git` with argv-only arguments (never a shell) in dir. dir may
// be "" for global commands (e.g. `git --version`).
func RunGit(ctx context.Context, dir string, args ...string) (GitResult, error) {
	// `git status` writes the index ("optional locks") whenever it refreshes
	// stat info; combined with our repo watcher that is a refresh feedback
	// loop (index write → git:changed → status → index write). Status is
	// read-only for our purposes, so disable the optional lock (this is what
	// tooling integrations like VS Code do).
	if len(args) > 0 && args[0] == "status" {
		args = append([]string{"--no-optional-locks"}, args...)
	}

	cmd := exec.CommandContext(ctx, "git", args...)

	if dir != "" {
		cmd.Dir = dir
	}

	// Never page; never open an editor mid-command.
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_EDITOR=:")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := GitResult{Stdout: stdout.String(), Stderr: stderr.String(), Spawned: true}

	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			res.Exit = exitError.ExitCode()

			return res, nil
		}

		// Spawn failure (no git on PATH etc.)
		return GitResult{Stderr: err.Error()}, err
	}

	return res, nil
}

// gitText runs git and returns (ok, stdout, stderr); ok=false on nonzero exit.
func gitText(ctx context.Context, dir string, args ...string) (bool, string, string, error) {
	res, err := RunGit(ctx, dir, args...)

	return res.Ok(), res.Stdout, res.Stderr, err
}

// gitVersion returns the human-friendly `git --version` output line.
func gitVersion(ctx context.Context) string {
	res, _ := RunGit(ctx, "", "--version")
	line := strings.TrimSpace(res.Stdout)

	if line == "" {
		line = strings.TrimSpace(res.Stderr)
	}

	return line
}
