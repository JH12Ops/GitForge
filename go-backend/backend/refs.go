package backend

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

/* Mirror of refs.rs: branches, tags, stashes, remotes, worktrees, status. */

const refFIELD = "\x1f"

// BranchesCommand mirrors fetch_branches: for-each-ref with tracking info.
func branchesCommand() []string {
	return []string{
		"for-each-ref", "--sort=-committerdate",
		"--format=" + joinSansSep([]string{
			"%(refname)", "%(objectname)", "%(HEAD)",
			"%(upstream:short)", "%(upstream:track)", "%(committerdate:unix)",
		}),
		"refs/heads", "refs/remotes",
	}
}

func joinSansSep(parts []string) string {
	// for-each-ref --format: literal \x1f between fields.
	var b strings.Builder

	for i, p := range parts {
		if i > 0 {
			b.WriteString(refFIELD)
		}

		b.WriteString(p)
	}

	return b.String()
}

func parseAheadBehind(track, keyword string) int {
	rest := track

	for {
		idx := strings.Index(rest, keyword)

		if idx == -1 {
			return 0
		}

		after := strings.TrimSpace(rest[idx+len(keyword):])
		digits := ""

		for _, r := range after {
			if r < '0' || r > '9' {
				break
			}

			digits += string(r)
		}

		if n, err := strconv.Atoi(digits); err == nil {
			return n
		}

		rest = after
	}
}

func groupFor(name string) string {
	if idx := strings.Index(name, "/"); idx != -1 {
		return name[:idx]
	}

	return ""
}

func parseBranches(stdout string, favorites map[string]bool) []WireBranch {
	branches := []WireBranch{}

	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimRight(raw, "\r\n ")

		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Split(line, refFIELD)
		get := func(i int) string {
			if i < len(fields) {
				return fields[i]
			}

			return ""
		}
		refname, sha, head, upstream, track, dateUnix :=
			get(0), get(1), get(2), get(3), get(4), get(5)

		if name, ok := strings.CutPrefix(refname, "refs/heads/"); ok {
			ahead := parseAheadBehind(track, "ahead ")
			behind := parseAheadBehind(track, "behind ")
			gone := strings.Contains(track, "[gone]")
			branch := WireBranch{
				ID:           "local:" + name,
				Name:         name,
				Kind:         "local",
				Group:        groupFor(name),
				TipSha:       sha,
				IsCurrent:    head == "*",
				IsFavorite:   favorites[name],
				LastActivity: parseUnixOrEpoch(dateUnix),
				LaneColorIdx: laneColorIndexFor(name),
			}

			if !gone && upstream != "" {
				branch.Upstream = strPtr(upstream)
			}

			branch.Ahead = ahead
			branch.Behind = behind
			branches = append(branches, branch)
			continue
		}

		if rest, ok := strings.CutPrefix(refname, "refs/remotes/"); ok {
			if rest == "HEAD" || strings.HasSuffix(rest, "/HEAD") {
				continue
			}

			remote, name := rest, rest

			if idx := strings.Index(rest, "/"); idx != -1 {
				remote = rest[:idx]
				name = rest[idx+1:]
			}

			branches = append(branches, WireBranch{
				ID:           "remote:" + rest,
				Name:         name,
				Kind:         "remote",
				Group:        groupFor(name),
				Remote:       strPtr(remote),
				TipSha:       sha,
				LastActivity: parseUnixOrEpoch(dateUnix),
				LaneColorIdx: laneColorIndexFor(name),
			})
		}
	}

	return branches
}

func parseUnixOrEpoch(raw string) string {
	if secs, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
		return formatUnixISO(secs)
	}

	return epochISO
}

// tagsCommand mirrors fetch_tags.
func tagsCommand() []string {
	return []string{
		"for-each-ref", "--sort=-creatordate",
		"--format=" + joinSansSep([]string{
			"%(refname)", "%(objectname)", "%(*objectname)",
			"%(objecttype)", "%(creatordate:unix)", "%(contents:subject)",
		}),
		"refs/tags",
	}
}

func parseTags(stdout string) []WireTag {
	tags := []WireTag{}

	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimRight(raw, "\r\n")

		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Split(line, refFIELD)
		get := func(i int) string {
			if i < len(fields) {
				return fields[i]
			}

			return ""
		}
		name, objectName, deref, objectType, dateUnix, subject :=
			get(0), get(1), get(2), get(3), get(4), get(5)
		name = strings.TrimPrefix(name, "refs/tags/")
		annotated := objectType == "tag"
		sha := objectName

		if annotated && deref != "" {
			sha = deref
		}

		tags = append(tags, WireTag{
			ID:        "tag:" + name,
			Name:      name,
			Sha:       sha,
			Message:   subject,
			Date:      parseUnixOrEpoch(dateUnix),
			Annotated: annotated,
		})
	}

	return tags
}

// stashesCommand mirrors fetch_stashes (uses `git stash list --format`).
func stashesCommand() []string {
	return []string{"stash", "list", "--format=" + joinSansSep([]string{
		"%gd", "%at", "%gs",
	})}
}

func stashBranchFrom(message string) string {
	for prefix, marker := range map[string]string{"WIP on ": ":", "On ": ":"} {
		if strings.HasPrefix(message, prefix) {
			rest := strings.TrimPrefix(message, prefix)

			if idx := strings.Index(rest, marker); idx != -1 {
				return rest[:idx]
			}
		}
	}

	return ""
}

func parseStashes(stdout string) []WireStash {
	stashes := []WireStash{}

	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimRight(raw, "\r\n")

		if strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Split(line, refFIELD)
		get := func(i int) string {
			if i < len(fields) {
				return fields[i]
			}

			return ""
		}
		id, unix, message := get(0), get(1), get(2)
		idx := strings.Index(id, "{")
		closing := strings.Index(id, "}")
		var index int

		if idx != -1 && closing > idx {
			if n, err := strconv.Atoi(id[idx+1 : closing]); err == nil {
				index = n
			}
		}

		stashes = append(stashes, WireStash{
			ID:      id,
			Index:   index,
			Message: message,
			Branch:  stashBranchFrom(message),
			Date:    parseUnixOrEpoch(unix),
		})
	}

	return stashes
}

// resolveStashBase backs each stash with its base commit (the first parent).
func resolveStashBase(ctx context.Context, cwd, stashID string) (*string, *string) {
	res, err := RunGit(ctx, cwd, "rev-parse", stashID+"^")

	if err != nil || !res.Ok() {
		return nil, nil
	}

	base := strings.TrimSpace(res.Stdout)
	res2, _ := RunGit(ctx, cwd, "log", "-1", "--format=%s", base)
	subject := strings.TrimSpace(res2.Stdout)

	return strPtr(base), strPtr(subject)
}

func countStashFiles(ctx context.Context, cwd, stashID string) int {
	res, _ := RunGit(ctx, cwd, "stash", "show", "--porcelain", stashID)
	count := 0

	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}

	return count
}

// remotesCommand mirrors fetch_remotes (`git remote -v`).
func parseRemotes(stdout string) []WireRemote {
	var remotes []WireRemote

	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)

		if line == "" {
			continue
		}

		parts := strings.Fields(line)

		if len(parts) < 2 {
			continue
		}

		name := parts[0]
		url := parts[1]
		kind := ""

		if len(parts) >= 3 {
			kind = strings.Trim(parts[2], "()")
		}

		var r *WireRemote

		for i := range remotes {
			if remotes[i].Name == name {
				r = &remotes[i]
				break
			}
		}

		if r == nil {
			remotes = append(remotes, WireRemote{Name: name})
			r = &remotes[len(remotes)-1]
		}

		switch kind {
		case "push":
			r.PushURL = url

		default: // fetch
			r.FetchURL = url
		}
	}

	// Pad missing push URLs empty (mirror of remotes tests).
	for i := range remotes {
		if remotes[i].PushURL == "" && strings.HasSuffix(remotes[i].FetchURL, ".git") {
			// remote lines sometimes omit (push); leave empty by design.
		}
	}

	return remotes
}

// worktreesCommand mirrors fetch_worktrees.
func parseWorktrees(stdout string, mainPath string) []WireWorktree {
	var worktrees []WireWorktree
	var current *WireWorktree
	idCounter := 0
	flush := func() {
		if current != nil {
			worktrees = append(worktrees, *current)
			current = nil
		}
	}

	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)

		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			path := strings.TrimPrefix(line, "worktree ")
			current = &WireWorktree{
				ID:     fmt.Sprintf("wt:%d", idCounter),
				Path:   path,
				IsMain: path == mainPath,
			}
			idCounter++

		case current != nil && strings.HasPrefix(line, "branch "):
			refname := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(refname, "refs/heads/")

		case current != nil && strings.HasPrefix(line, "locked"):
			current.IsLocked = true
		}
	}

	flush()

	return worktrees
}

/* ------------------------------- status -------------------------------- */

type NumstatEntry struct {
	Additions int
	Deletions int
}

// parseNumstatZ mirrors refs.rs parse_numstat for `--numstat -z` output.
func parseNumstatZ(stdout string) map[string]NumstatEntry {
	m := map[string]NumstatEntry{}
	tokens := strings.Split(stdout, "\x00")
	i := 0

	for i < len(tokens) {
		head := tokens[i]

		if head == "" || !strings.Contains(head, "\t") {
			i++
			continue
		}

		parts := strings.SplitN(head, "\t", 3)

		for len(parts) < 3 {
			parts = append(parts, "")
		}

		addRaw, delRaw, inlinePath := parts[0], parts[1], parts[2]
		additions := parseCount(addRaw)
		deletions := parseCount(delRaw)
		var path string

		if inlinePath != "" {
			i++
			path = inlinePath
		} else {
			// Rename: next two tokens are old then new path.
			i++ // advance
			if i+1 < len(tokens) {
				path = tokens[i+1]
				i += 2
			}
		}

		if path != "" {
			m[path] = NumstatEntry{Additions: additions, Deletions: deletions}
		}
	}

	return m
}

func parseCount(raw string) int {
	if raw == "-" || raw == "" {
		return 0
	}

	n, err := strconv.Atoi(raw)

	if err != nil {
		return 0
	}

	return n
}

func mapCode(code byte) WireFileStatus {
	switch code {
	case 'A':
		return StatusAdded

	case 'D':
		return StatusDeleted

	case 'R':
		return StatusRenamed

	case 'C':
		return StatusCopied

	case 'U':
		return StatusConflicted

	default:
		// 'M', 'T' and anything else
		return StatusModified
	}
}

type StatusInput struct {
	Porcelain string
	Staged    map[string]NumstatEntry
	Unstaged  map[string]NumstatEntry
}

func parseStatus(input StatusInput) []WireWorkingTreeFile {
	files := []WireWorkingTreeFile{}
	id := 0
	push := func(path string, status WireFileStatus, staged bool, counts NumstatEntry, oldPath string, isSubmodule bool) {
		fileName, directory := splitFilePath(path)
		file := WireWorkingTreeFile{
			ID:          fmt.Sprintf("file:%d", id),
			Path:        path,
			FileName:    fileName,
			Directory:   directory,
			Status:      status,
			Staged:      staged,
			Additions:   counts.Additions,
			Deletions:   counts.Deletions,
			OldPath:     oldPath,
			IsSubmodule: isSubmodule,
		}
		files = append(files, file)
		id++
	}

	tokens := strings.Split(input.Porcelain, "\x00")
	i := 0

	for i < len(tokens) {
		if i >= len(tokens) {
			break
		}

		line := tokens[i]

		if line == "" {
			i++
			continue
		}

		kind := line[0]

		switch kind {
		case '1':
			// "1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>"
			parts := strings.Split(line, " ")

			if len(parts) < 9 {
				i++
				continue
			}

			xy := parts[1]
			sub := parts[2]
			path := strings.Join(parts[8:], " ")
			isSubmodule := strings.HasPrefix(sub, "S")
			var x, y byte = '.', '.'

			if len(xy) >= 2 {
				x, y = xy[0], xy[1]
			}

			if x != '.' {
				push(path, mapCode(x), true, input.Staged[path], "", isSubmodule)
			}

			if y != '.' {
				push(path, mapCode(y), false, input.Unstaged[path], "", isSubmodule)
			}

			i++

		case '2':
			// "2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <Xscore> <path>" + NUL + <origPath>
			parts := strings.Split(line, " ")

			if len(parts) < 10 {
				i++
				continue
			}

			xy := parts[1]
			sub := parts[2]
			path := strings.Join(parts[9:], " ")
			origPath := ""

			if i+1 < len(tokens) {
				origPath = tokens[i+1]
				i++ // consume origPath
			}

			isSubmodule := strings.HasPrefix(sub, "S")
			var x, y byte = '.', '.'

			if len(xy) >= 2 {
				x, y = xy[0], xy[1]
			}

			if x != '.' {
				push(path, mapCode(x), true, input.Staged[path], origPath, isSubmodule)
			}

			if y != '.' {
				push(path, mapCode(y), false, input.Unstaged[path], "", isSubmodule)
			}

			i++

		case 'u':
			// "u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>"
			parts := strings.Split(line, " ")

			if len(parts) < 11 {
				i++
				continue
			}

			sub := parts[2]
			path := strings.Join(parts[10:], " ")
			push(path, StatusConflicted, true, NumstatEntry{}, "", strings.HasPrefix(sub, "S"))
			i++

		case '?':
			// "? <path>"
			path := strings.TrimPrefix(line, "? ")

			if path != "" {
				push(path, StatusUntracked, false, NumstatEntry{}, "", false)
			}

			i++

		default:
			// '!' ignored files, silently skipped (mirror).
			i++
		}
	}

	return files
}
