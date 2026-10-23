package backend

import (
	"fmt"
	"strings"
)

// JS-compatible hashing (32-bit signed overflow over UTF-16 units) — mirrors
// log.rs hash_string, which itself mirrors the TS hashString. This keeps lane
// and avatar colors stable across backend swaps.
func hashString(value string) uint32 {
	var hash int32

	for _, r := range value {
		// Encode UTF-16 units: most Go source strings we hash are BMP, but
		// correct hashing needs surrogate pairs, so convert rune-by-rune.
		for _, unit := range utf16Units(r) {
			hash = hash*31 + int32(unit)
		}
	}

	return uint32(hash)
}

// utf16Units yields the UTF-16 code units of a rune (1 or 2 units).
func utf16Units(r rune) []uint16 {
	if r <= 0xFFFF && !(r >= 0xD800 && r <= 0xDFFF) {
		return []uint16{uint16(r)}
	}

	if r > 0xFFFF {
		v := uint32(r - 0x10000)
		hi := uint16(0xD800 + (v >> 10))
		lo := uint16(0xDC00 + (v & 0x3FF))

		return []uint16{hi, lo}
	}

	return nil
}

var avatarColors = []string{
	"#a371f7", "#3fb950", "#58a6ff", "#f0883e",
	"#db61a2", "#e3b341", "#56d4dd", "#ff7b72",
}

func laneColorIndexFor(name string) int {
	return int(hashString(name) % 8)
}

// Mirrors initialsFor in shared/git/avatar.ts.
func initialsFor(name string) string {
	parts := strings.Fields(strings.TrimSpace(name))

	switch len(parts) {
	case 0:
		return "?"

	case 1:
		return upperFirstN(parts[0], 2)

	default:
		first := runeStr(parts[0], 0)
		last := runeStr(parts[len(parts)-1], 0)

		return strings.ToUpper(first + last)
	}
}

func runeStr(s string, i int) string {
	runes := []rune(s)

	if i < len(runes) {
		return string(runes[i])
	}

	return "?"
}

func upperFirstN(s string, n int) string {
	runes := []rune(s)

	if len(runes) < n {
		return strings.ToUpper(s)
	}

	return strings.ToUpper(string(runes[:n]))
}

// Mirrors githubUsernameFromEmail + githubAvatarUrl in shared/git/avatar.ts.
func githubUsernameFromEmail(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))

	if !strings.HasSuffix(normalized, "@users.noreply.github.com") {
		return ""
	}

	local := strings.TrimSuffix(normalized, "@users.noreply.github.com")

	if idx := strings.Index(local, "+"); idx != -1 {
		prefix, rest := local[:idx], local[idx+1:]

		if prefix == "" || !isAllDigitsPrefix(prefix) || rest == "" ||
			strings.ContainsAny(rest, "+@") {
			return ""
		}

		return rest
	}

	if local == "" || strings.ContainsAny(local, "@") {
		return ""
	}

	return local
}

func isAllDigitsPrefix(s string) bool {
	if s == "" {
		return false
	}

	return isAllDigits(s)
}

func githubAvatarURL(email string, size int) string {
	user := githubUsernameFromEmail(email)

	if user == "" {
		return ""
	}

	if size < 1 {
		size = 1
	}

	if size > 460 {
		size = 460
	}

	return fmt.Sprintf("https://github.com/%s.png?size=%d", user, size)
}

// AuthorRegistry deduplicates authors by email with stable palette colors
// (mirror of log.rs AuthorRegistry).
type AuthorRegistry struct {
	byEmail map[string]WireAuthor
}

func NewAuthorRegistry() *AuthorRegistry {
	return &AuthorRegistry{byEmail: map[string]WireAuthor{}}
}

func (r *AuthorRegistry) Resolve(name, email string) WireAuthor {
	key := strings.ToLower(email)

	if key == "" {
		key = strings.ToLower(name)
	}

	if existing, ok := r.byEmail[key]; ok {
		return existing
	}

	display := name

	if display == "" {
		display = email
	}

	author := WireAuthor{
		Name:      display,
		Email:     email,
		AvatarURL: githubAvatarURL(email, 80),
		Initials:  initialsFor(display),
		Color:     avatarColors[int(hashString(key))%len(avatarColors)],
	}
	r.byEmail[key] = author

	return author
}

func (r *AuthorRegistry) List() []WireAuthor {
	authors := make([]WireAuthor, 0, len(r.byEmail))

	for _, a := range r.byEmail {
		authors = append(authors, a)
	}

	return authors
}
