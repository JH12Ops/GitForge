package backend

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
)

/* Image preview (port of image.rs): turns before/after versions of a raster
or vector file into data-URL sides for the diff panel. */

// maxImageBytes mirrors MAX_IMAGE_BYTES: 8 MiB covers photos/screenshots.
const maxImageBytes = 8 * 1024 * 1024

type imageSide struct {
	DataURL   string `json:"dataUrl"`
	SizeBytes int    `json:"sizeBytes"`
}

type ImagePreviewResult struct {
	Path     string     `json:"path"`
	Mime     string     `json:"mime"`
	OldImage *imageSide `json:"oldImage,omitempty"`
	NewImage *imageSide `json:"newImage,omitempty"`
	TooLarge bool       `json:"tooLarge"`
}

// imageMime maps an extension → MIME for previewable files (mirror of
// image.rs::image_mime, which mirrors src/lib/imageFiles.ts). The Rust
// version splits the whole path on the last '.', so the extension may carry
// slashes in pathological names — same behaviour here.
func imageMime(path string) string {
	ext := path

	if idx := strings.LastIndex(path, "."); idx >= 0 {
		ext = path[idx+1:]
	}

	ext = strings.ToLower(ext)

	switch ext {
	case "png":
		return "image/png"

	case "jpg", "jpeg":
		return "image/jpeg"

	case "gif":
		return "image/gif"

	case "webp":
		return "image/webp"

	case "bmp":
		return "image/bmp"

	case "ico", "cur":
		return "image/x-icon"

	case "avif":
		return "image/avif"

	case "svg":
		return "image/svg+xml"

	default:
		return ""
	}
}

func toImageSide(mime string, data []byte) *imageSide {
	return &imageSide{
		DataURL:   "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
		SizeBytes: len(data),
	}
}

func imageTooLarge(n int) bool { return n > maxImageBytes }

// gitBlobBytes tolerates absence (allow-failure): mirrors read_git_blob,
// which demands a clean exit AND non-empty output (empty blob ≡ missing).
func gitBlobBytes(ctx context.Context, cwd, spec string) ([]byte, bool) {
	res, gerr := runActionGit(ctx, cwd, runOpts{args: []string{"show", spec}, allowFailure: true})

	if gerr != nil || res.exit != 0 || res.stdout == "" {
		return nil, false
	}

	return []byte(res.stdout), true
}

// validateShaPort mirrors image.rs::validate_sha: reject control chars and
// rev/path separators from `git show`, allow hex shas and named refs. Note
// the Rust char set allows `{` but not `}` inside named refs, and rejects a
// plain space (not tab) — mirrored verbatim since the outer guard catches
// `stash@{0}`-style refs before the inner check anyway.
func validateShaPort(raw string) (string, *GitError) {
	trimmed := strings.TrimSpace(raw)

	if trimmed == "" {
		return "", nil
	}

	if strings.ContainsAny(trimmed, "\x00\n\r:") || len(trimmed) > 256 ||
		strings.Contains(trimmed, "..") || strings.Contains(trimmed, " ") {
		isHex := len(trimmed) <= 40

		for i := 0; i < len(trimmed) && isHex; i++ {
			c := trimmed[i]

			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				isHex = false
			}
		}

		isNamedRef := true

		for i := 0; i < len(trimmed) && isNamedRef; i++ {
			c := trimmed[i]

			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
				c == '-' || c == '_' || c == '/' || c == '@' || c == '^' || c == '~' || c == '{') {
				isNamedRef = false
			}
		}

		if !isHex && !isNamedRef {
			return "", unknownErr(`Invalid revision "` + trimmed + `".`)
		}
	}

	return trimmed, nil
}

func imageCommitParents(ctx context.Context, cwd, sha string) []string {
	res, err := RunGit(ctx, cwd, "rev-list", "--parents", "-n", "1", sha)

	if err != nil {
		return nil
	}

	fields := strings.Fields(res.Stdout)

	if len(fields) <= 1 {
		return nil
	}

	return fields[1:]
}

// FetchImagePreview mirrors fetch_image_preview: the source selects the
// before/after revisions.
func FetchImagePreview(ctx context.Context, repoPath string, req DiffRequestWire) (*ImagePreviewResult, *GitError) {
	safe, gerr := assertPathInsideRepo(repoPath, req.Path)

	if gerr != nil {
		return nil, gerr
	}

	mime := imageMime(safe)

	if mime == "" {
		return nil, unknownErr("Not an image file.")
	}

	sha := ""

	if req.Sha != nil {
		sha = *req.Sha
	}

	toSha := ""

	if req.ToSha != nil {
		toSha = *req.ToSha
	}

	sha, gerr = validateShaPort(sha)

	if gerr != nil {
		return nil, gerr
	}

	toSha, gerr = validateShaPort(toSha)

	if gerr != nil {
		return nil, gerr
	}

	var oldBytes, newBytes []byte

	switch req.Source {
	case "worktree":
		if b, ok := gitBlobBytes(ctx, repoPath, "HEAD:"+safe); ok {
			oldBytes = b
		}

		// An existing-but-empty worktree file still counts (Some(vec![])).
		if data, rerr := os.ReadFile(filepath.Join(repoPath, safe)); rerr == nil {
			newBytes = data
		}

	case "index":
		if b, ok := gitBlobBytes(ctx, repoPath, "HEAD:"+safe); ok {
			oldBytes = b
		}

		if b, ok := gitBlobBytes(ctx, repoPath, ":"+safe); ok {
			newBytes = b
		}

	case "commit":
		rev := derefEmpty(sha, "HEAD")
		parents := imageCommitParents(ctx, repoPath, rev)

		if len(parents) > 0 {
			if b, ok := gitBlobBytes(ctx, repoPath, parents[0]+":"+safe); ok {
				oldBytes = b
			}
		}

		if b, ok := gitBlobBytes(ctx, repoPath, rev+":"+safe); ok {
			newBytes = b
		}

	default: // range
		from := derefEmpty(sha, "HEAD~1")
		to := derefEmpty(toSha, derefEmpty(sha, "HEAD"))

		if b, ok := gitBlobBytes(ctx, repoPath, from+":"+safe); ok {
			oldBytes = b
		}

		if b, ok := gitBlobBytes(ctx, repoPath, to+":"+safe); ok {
			newBytes = b
		}
	}

	if (oldBytes != nil && imageTooLarge(len(oldBytes))) ||
		(newBytes != nil && imageTooLarge(len(newBytes))) {
		return &ImagePreviewResult{Path: safe, Mime: mime, TooLarge: true}, nil
	}

	// Both sides missing (commit doesn't touch the path, untracked binary
	// git can't resolve): empty result so the panel falls back to the
	// binary message rather than an error.
	result := &ImagePreviewResult{Path: safe, Mime: mime}

	if oldBytes == nil && newBytes == nil {
		return result, nil
	}

	if oldBytes != nil {
		result.OldImage = toImageSide(mime, oldBytes)
	}

	if newBytes != nil {
		result.NewImage = toImageSide(mime, newBytes)
	}

	return result, nil
}
