package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

/* Conflict marker parsing (port of diff.rs parse_conflict_markers +
apply_conflict_resolution) — feeds the block-level conflict resolver. */

type conflictBlock struct {
	index       int
	oursLabel   string
	theirsLabel string
	ours        string
	theirs      string
	base        string
	startLine   int // 1-based, inclusive of the `<<<<<<<` line
	endLine     int // 1-based, exclusive of the block content, at the `>>>>>>>` line +1
}

type conflictParse struct {
	blocks     []conflictBlock
	hasMarkers bool
}

// parseConflictMarkers parses merge-conflict markers incl. the `|||||||`
// base (mirror of parse_conflict_markers).
func parseConflictMarkers(content string) conflictParse {
	splitLines := strings.Split(content, "\n")
	lines := make([]string, len(splitLines))

	for i, l := range splitLines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}

	var blocks []conflictBlock
	sawMarker := false
	i := 0

	for i < len(lines) {
		rest, ok := strings.CutPrefix(lines[i], "<<<<<<< ")

		if !ok || rest == "" {
			i++
			continue
		}

		startLine := i + 1
		oursLabel := strings.TrimSpace(rest)
		sawMarker = true
		i++
		var oursLines []string
		base := ""

		for i < len(lines) && !strings.HasPrefix(lines[i], "=======") {
			// TS requires a label after `|||||||`; a bare marker is content.
			if baseRest, ok := strings.CutPrefix(lines[i], "||||||| "); ok {
				if baseRest == "" {
					oursLines = append(oursLines, lines[i])
					i++
					continue
				}

				i++
				var baseLines []string

				for i < len(lines) && !strings.HasPrefix(lines[i], "=======") {
					baseLines = append(baseLines, lines[i])
					i++
				}

				base = strings.Join(baseLines, "\n")
				break
			}

			oursLines = append(oursLines, lines[i])
			i++
		}

		if i >= len(lines) || !strings.HasPrefix(lines[i], "=======") {
			break
		}

		i++
		var theirsLines []string

		for i < len(lines) && !strings.HasPrefix(lines[i], ">>>>>>>") {
			theirsLines = append(theirsLines, lines[i])
			i++
		}

		endLabel, ok := strings.CutPrefix(lines[i], ">>>>>>> ")

		if !ok {
			break
		}

		if endLabel == "" {
			break
		}

		blocks = append(blocks, conflictBlock{
			index:       len(blocks),
			oursLabel:   oursLabel,
			theirsLabel: strings.TrimSpace(endLabel),
			ours:        strings.Join(oursLines, "\n"),
			theirs:      strings.Join(theirsLines, "\n"),
			base:        base,
			startLine:   startLine,
			endLine:     i + 1,
		})
		i++
	}

	if blocks == nil {
		blocks = []conflictBlock{}
	}

	return conflictParse{blocks: blocks, hasMarkers: sawMarker}
}

// applyConflictResolution replaces one block's content with the chosen side
// (mirror of apply_conflict_resolution).
func applyConflictResolution(content string, blockIndex int, side string) (string, error) {
	parsed := parseConflictMarkers(content)

	if blockIndex < 0 || blockIndex >= len(parsed.blocks) {
		return "", fmt.Errorf("Conflict block %d not found", blockIndex)
	}

	block := parsed.blocks[blockIndex]
	replacement := block.ours

	if side == "theirs" {
		replacement = block.theirs
	}

	lines := strings.Split(content, "\n")
	var out []string
	out = append(out, lines[:block.startLine-1]...)
	out = append(out, strings.Split(replacement, "\n")...)
	out = append(out, lines[block.endLine:]...)

	return strings.Join(out, "\n"), nil
}

/* ------------------------- conflict read endpoint -------------------------- */

// WireConflictBlock mirrors shared/git/models.ts ConflictBlock (Rust
// ConflictBlock serde: base skipped when absent).
type WireConflictBlock struct {
	Index       int     `json:"index"`
	OursLabel   string  `json:"oursLabel"`
	TheirsLabel string  `json:"theirsLabel"`
	Ours        string  `json:"ours"`
	Theirs      string  `json:"theirs"`
	Base        *string `json:"base,omitempty"`
	StartLine   int     `json:"startLine"`
	EndLine     int     `json:"endLine"`
}

// ConflictVersionsWire mirrors ConflictVersions (all nullable, always sent).
type ConflictVersionsWire struct {
	Base   *string `json:"base"`
	Ours   *string `json:"ours"`
	Theirs *string `json:"theirs"`
}

// ConflictResult mirrors ConflictResult.
type ConflictResult struct {
	Path       string               `json:"path"`
	Binary     bool                 `json:"binary"`
	HasMarkers bool                 `json:"hasMarkers"`
	Blocks     []WireConflictBlock  `json:"blocks"`
	Versions   ConflictVersionsWire `json:"versions"`
}

// maxConflictFileBytes mirrors the 512 KiB guard: larger files are surfaced
// as binary so the UI stops parsing markers.
const maxConflictFileBytes = 512 * 1024

func emptyConflict(path string, binary bool) *ConflictResult {
	return &ConflictResult{
		Path:     path,
		Binary:   binary,
		Blocks:   []WireConflictBlock{},
		Versions: ConflictVersionsWire{},
	}
}

// readStageVersion mirrors read_stage_version: `git show :<stage>:<path>`,
// empty output treated as absent. Errors are swallowed (an unresolved path
// has no staged versions).
func readStageVersion(ctx context.Context, cwd, path string, stage int) *string {
	res, gerr := runActionGit(ctx, cwd, runOpts{
		args:         []string{"show", fmt.Sprintf(":%d:%s", stage, path)},
		allowFailure: true,
	})

	if gerr != nil || res.exit != 0 || res.stdout == "" {
		return nil
	}

	out := res.stdout

	return &out
}

// wireBlocks converts the parser output to the camelCase wire shape.
func wireBlocks(blocks []conflictBlock) []WireConflictBlock {
	wire := make([]WireConflictBlock, 0, len(blocks))

	for _, b := range blocks {
		wb := WireConflictBlock{
			Index:       b.index,
			OursLabel:   b.oursLabel,
			TheirsLabel: b.theirsLabel,
			Ours:        b.ours,
			Theirs:      b.theirs,
			StartLine:   b.startLine,
			EndLine:     b.endLine,
		}

		if b.base != "" {
			base := b.base
			wb.Base = &base
		}

		wire = append(wire, wb)
	}

	return wire
}

// FetchConflict mirrors diff.rs fetch_conflict: read the working-tree copy,
// parse markers, and pull :1:/:2:/:3: stage versions for the side editor.
func FetchConflict(ctx context.Context, repoPath string, path string) (*ConflictResult, *GitError) {
	safe, gerr := assertPathInsideRepo(repoPath, path)

	if gerr != nil {
		return nil, gerr
	}

	full := filepath.Join(repoPath, safe)

	if fi, serr := os.Stat(full); serr != nil {
		// Missing working-tree file (deleted during the conflict): empty.
		return emptyConflict(safe, false), nil
	} else if fi.Size() > maxConflictFileBytes {
		return emptyConflict(safe, true), nil
	}

	content, rerr := os.ReadFile(full)

	if rerr != nil {
		return emptyConflict(safe, false), nil
	}

	text := string(content)

	if strings.ContainsRune(text, '\x00') {
		return emptyConflict(safe, true), nil
	}

	parsed := parseConflictMarkers(text)
	versions := ConflictVersionsWire{
		Base:   readStageVersion(ctx, repoPath, safe, 1),
		Ours:   readStageVersion(ctx, repoPath, safe, 2),
		Theirs: readStageVersion(ctx, repoPath, safe, 3),
	}

	return &ConflictResult{
		Path:       safe,
		Binary:     false,
		HasMarkers: parsed.hasMarkers,
		Blocks:     wireBlocks(parsed.blocks),
		Versions:   versions,
	}, nil
}
