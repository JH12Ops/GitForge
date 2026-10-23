package backend

import (
	"context"
	"os"
	"time"
)

// PickAndOpenFunc is injected by main (Wails dialog lives in package main to
// keep this package UI-free). It must show a native folder picker and return
// ""/nil when the user cancels.
var PickAndOpenFunc func() (string, error)

func pickAndOpen(ctx context.Context, opts SnapshotOptions) any {
	if PickAndOpenFunc == nil {
		return OpenRepoResult{OK: false, Error: gitErr(ErrCodeCancelled, "", nil).Serialize()}
	}

	path, err := PickAndOpenFunc()

	if err != nil || path == "" {
		// Cancelled is a normal outcome, not an error payload.
		return map[string]any{"cancelled": true}
	}

	res, gerr := OpenRepo(ctx, path, opts)

	if gerr != nil {
		return OpenRepoResult{OK: false, Error: gerr.Serialize()}
	}

	return res
}

/* Shared helpers: the tiny indirections exist so tests can swap behavior. */

func newTicker(interval time.Duration) *time.Ticker {
	return time.NewTicker(interval)
}

func osReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Diagnostic log sink: like a one-ring journal for release builds (no
// devtools). Gateway access entries and renderer-relayed errors both land
// here; main.go sets the path once at startup.
var logSink string

func osWriteFile(path string, data []byte, append bool) error {
	flags := os.O_WRONLY | os.O_CREATE

	if append {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	f, err := os.OpenFile(path, flags, 0o600)

	if err != nil {
		return err
	}

	defer f.Close()
	_, err = f.Write(data)

	return err
}

// logNote mirrors a Go-side event into the sink.
func logNote(msg string) { appendLog(msg + "\n") }

// SetLogSink points the diagnostic sink (nil/empty disables it).
func SetLogSink(path string) { logSink = path }
