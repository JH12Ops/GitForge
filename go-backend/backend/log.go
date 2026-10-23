package backend

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Field/record separators — identical to log.rs FIELD/RECORD, whose format is
// the shared contract with the TS LOG_FORMAT.
const (
	LogFIELD  = "\x1f"
	LogRECORD = "\x1e"
)

// logFormat mirrors log.rs log_format(): `%H␟%P␟%an␟%ae␟%cn␟%ce␟%at␟%D␟%s␟%b␞`
func logFormat() string {
	fields := []string{"%H", "%P", "%an", "%ae", "%cn", "%ce", "%at", "%D", "%s", "%b"}

	return strings.Join(fields, LogFIELD) + LogRECORD
}

// parseDecorations maps `%D` decoration tokens onto WireRefs, using the
// known-ref registry built from for-each-ref so short and full decorations
// classify identically (see the classification tests in log.rs).
func parseDecorations(decorations string, registry *RefRegistry) []WireRef {

	var refs []WireRef
	dec := strings.TrimSpace(decorations)

	if dec == "" {
		return nil
	}

	// "HEAD -> main, origin/main, tag: v1.0"
	headTarget := ""
	tokens := dec

	if idx := strings.Index(dec, "HEAD -> "); idx == 0 {
		rest := strings.TrimPrefix(dec, "HEAD -> ")

		if comma := strings.Index(rest, ", "); comma != -1 {
			headTarget = rest[:comma]
			tokens = rest
		} else {
			headTarget = rest
			tokens = rest
		}
	}

	for _, raw := range strings.Split(tokens, ", ") {
		token := strings.TrimSpace(raw)

		if token == "" {
			continue
		}

		if strings.EqualFold(token, "HEAD") {
			// Detached HEAD looks like a pseudo ref; hide it (mirror of log.rs).
			continue
		}

		name := token
		typ := ""

		if strings.HasPrefix(name, "tag: ") {
			// Annotated tags decorate as `tag: v1.0`; strip the prefix.
			name = strings.TrimPrefix(name, "tag: ")
			typ = RefTag
		} else {
			typ = registry.TypeFor(name)
		}

		if typ == "" {
			continue // unknown decoration token
		}

		if typ == RefRemoteBranch && strings.HasSuffix(name, "/HEAD") {
			// Symref remotes (origin/HEAD) are pointers, not branch rows.
			continue
		}

		ref := WireRef{
			Type:  typ,
			Name:  name,
			Label: formatRefLabel(name, typ),
		}

		if headTarget != "" && sameRefName(name, headTarget, typ) {
			isHead := true
			ref.IsHead = &isHead
		}

		refs = append(refs, ref)
	}

	return refs
}

// sameRefName compares a decoration token with the HEAD target considering
// full/short naming differences.
func sameRefName(name, target, typ string) bool {
	if name == target {
		return true
	}

	// "refs/heads/main" vs "main"; remote short handled by label rules.
	return formatRefLabel(name, typ) == formatRefLabel(target, typ)
}

// formatRefLabel mirrors shared/git/refLabel.ts formatRefLabel.
func formatRefLabel(name string, typ WireRefType) string {
	label := strings.TrimSpace(name)

	switch {
	case strings.HasPrefix(label, "refs/remotes/"):
		label = strings.TrimPrefix(label, "refs/remotes/")

		if i := strings.Index(label, "/"); i != -1 {
			label = label[i+1:]
		}

	case strings.HasPrefix(label, "refs/heads/"):
		label = strings.TrimPrefix(label, "refs/heads/")

	case strings.HasPrefix(label, "refs/tags/"):
		label = strings.TrimPrefix(label, "refs/tags/")

	case label == "refs/stash" || typ == RefStash:
		return "stash"

	case typ == RefRemoteBranch:
		if i := strings.Index(label, "/"); i != -1 {
			label = label[i+1:]
		}
	}

	return label
}

// ParseCommits parses `git log` output in the shared FIELD/RECORD format and
// populates the author registry. Returns commits in the log order.
func ParseCommits(stdout string, authors *AuthorRegistry, refs *RefRegistry) []WireCommit {
	commits := []WireCommit{}

	for _, block := range strings.Split(stdout, LogRECORD) {
		block = strings.Trim(block, "\n\r")

		if strings.TrimSpace(block) == "" {
			continue
		}

		fields := strings.Split(block, LogFIELD)
		get := func(i int) string {
			if i < len(fields) {
				return fields[i]
			}

			return ""
		}
		sha := get(0)

		if sha == "" {
			continue
		}

		parents := splitNonEmpty(get(1))

		if parents == nil {
			parents = []string{}
		}

		author := authors.Resolve(get(2), get(3))
		committer := authors.Resolve(get(4), get(5))
		dateISO := parseGitDate(get(6))
		refs := parseDecorations(get(7), refs)

		if refs == nil {
			refs = []WireRef{} // JSON must be [], never null (TS does refs.filter)
		}

		subject := get(8)
		body := get(9)

		shortSha := sha

		if len(shortSha) > 7 {
			shortSha = shortSha[:7]
		}

		commits = append(commits, WireCommit{
			Sha:       sha,
			ShortSha:  shortSha,
			Parents:   parents,
			Subject:   subject,
			Body:      body,
			Author:    author,
			Committer: committer,
			Date:      dateISO,
			Refs:      refs,
			IsMerge:   len(parents) > 1,
		})
	}

	return commits
}

func splitNonEmpty(s string) []string {
	var out []string

	for _, p := range strings.Fields(s) {
		p = strings.TrimSpace(p)

		if p != "" {
			out = append(out, p)
		}
	}

	return out
}

// RefRegistry maps decoration name shapes to WireRefType values. Built once
// per call from `git for-each-ref` so both `refs/heads/...` and short forms
// classify identically (mirror of log.rs's ref lookup).
type RefRegistry struct {
	count []refEntry
}

type refEntry struct {
	short string
	typ   WireRefType
}

// LoadRefRegistry builds the decoration→type registry for one repository
// (`git for-each-ref` inside cwd), so both `refs/heads/...` and short forms
// classify identically (mirror of log.rs's ref lookup).
func LoadRefRegistry(ctx context.Context, cwd string) *RefRegistry {
	reg := &RefRegistry{}
	res, _ := RunGit(ctx, cwd, "for-each-ref",
		"--format=%(refname)", "refs/heads", "refs/remotes", "refs/tags", "refs/stash")

	for _, refname := range strings.Split(res.Stdout, "\n") {
		refname = strings.TrimSpace(refname)

		if refname == "" {
			continue
		}

		short := refname
		typ := ""

		switch {
		case strings.HasPrefix(refname, "refs/heads/"):
			short = strings.TrimPrefix(refname, "refs/heads/")
			typ = RefLocalBranch

		case strings.HasPrefix(refname, "refs/remotes/"):
			short = strings.TrimPrefix(refname, "refs/remotes/")
			typ = RefRemoteBranch

		case strings.HasPrefix(refname, "refs/tags/"):
			short = strings.TrimPrefix(refname, "refs/tags/")
			typ = RefTag

		case refname == "refs/stash":
			// Special: stash decorations arrive as text, not via registry.
			continue
		}

		if typ != "" {
			reg.count = append(reg.count, refEntry{short, typ})
		}
	}

	return reg
}

func (r *RefRegistry) TypeFor(decoration string) WireRefType {
	if r == nil {
		return ""
	}

	d := strings.TrimSpace(decoration)

	for _, e := range r.count {
		if e.short == d {
			return e.typ
		}
	}

	switch {
	case strings.HasPrefix(d, "refs/heads/"):
		return RefLocalBranch

	case strings.HasPrefix(d, "refs/remotes/"):
		return RefRemoteBranch

	case strings.HasPrefix(d, "refs/tags/"):
		return RefTag

	case strings.HasPrefix(d, "tag: "):
		return RefTag
	}

	return ""
}

var _ = fmt.Sprintf
var _ = strconv.Itoa
